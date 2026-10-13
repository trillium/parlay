// Package qr is a small, dependency-free QR Code encoder (ISO/IEC 18004):
// byte mode, error-correction level L, versions 1-10 — enough for a pairing
// URL of up to 271 bytes. It exists so the server can print a scannable
// pairing code at start without adding a module dependency.
package qr

import (
	"errors"
	"strings"
)

// Code is a rendered symbol; true is a dark module.
type Code struct {
	Size    int
	modules [][]bool
}

// Dark reports whether the module at (x, y) is dark. Out of range is light,
// which is what makes the quiet zone free.
func (c *Code) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
		return false
	}
	return c.modules[y][x]
}

// ecBlocks describes one version at level L: EC codewords per block and the
// data codewords of each block, in order.
type ecBlocks struct {
	ecPer int
	data  []int
}

var levelL = []ecBlocks{
	1:  {7, []int{19}},
	2:  {10, []int{34}},
	3:  {15, []int{55}},
	4:  {20, []int{80}},
	5:  {26, []int{108}},
	6:  {18, []int{68, 68}},
	7:  {20, []int{78, 78}},
	8:  {24, []int{97, 97}},
	9:  {30, []int{116, 116}},
	10: {18, []int{68, 68, 69, 69}},
}

var alignPos = [][]int{
	1: {}, 2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34},
	7: {6, 22, 38}, 8: {6, 24, 42}, 9: {6, 26, 46}, 10: {6, 28, 50},
}

// MaxBytes is the largest payload Encode accepts.
const MaxBytes = 271

func dataCap(v int) int {
	n := 0
	for _, d := range levelL[v].data {
		n += d
	}
	return n
}

// Encode renders text as a QR code, choosing the smallest version that fits.
func Encode(text string) (*Code, error) {
	payload := []byte(text)
	version := 0
	for v := 1; v <= 10; v++ {
		hdr := 12
		if v >= 10 {
			hdr = 20
		}
		if (hdr+8*len(payload)+7)/8 <= dataCap(v) {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, errors.New("qr: payload too long")
	}
	cw := interleave(version, dataCodewords(version, payload))
	size := 17 + 4*version
	b := newBuilder(size, version)
	b.drawFunctionPatterns()
	b.drawData(cw)
	best, bestScore := 0, -1
	for m := 0; m < 8; m++ {
		b.applyMask(m)
		b.drawFormat(m)
		s := b.penalty()
		if bestScore < 0 || s < bestScore {
			best, bestScore = m, s
		}
		b.applyMask(m) // XOR is its own inverse
	}
	b.applyMask(best)
	b.drawFormat(best)
	return &Code{Size: size, modules: b.mod}, nil
}

type bitBuf struct{ bits []bool }

func (b *bitBuf) add(val, n int) {
	for i := n - 1; i >= 0; i-- {
		b.bits = append(b.bits, (val>>i)&1 == 1)
	}
}

func dataCodewords(v int, payload []byte) []byte {
	var bb bitBuf
	bb.add(0b0100, 4)
	if v >= 10 {
		bb.add(len(payload), 16)
	} else {
		bb.add(len(payload), 8)
	}
	for _, c := range payload {
		bb.add(int(c), 8)
	}
	capBits := dataCap(v) * 8
	term := capBits - len(bb.bits)
	if term > 4 {
		term = 4
	}
	bb.add(0, term)
	for len(bb.bits)%8 != 0 {
		bb.bits = append(bb.bits, false)
	}
	out := make([]byte, len(bb.bits)/8)
	for i, bit := range bb.bits {
		if bit {
			out[i/8] |= 0x80 >> (i % 8)
		}
	}
	for pad := byte(0xEC); len(out) < dataCap(v); {
		out = append(out, pad)
		if pad == 0xEC {
			pad = 0x11
		} else {
			pad = 0xEC
		}
	}
	return out
}

func interleave(v int, data []byte) []byte {
	blk := levelL[v]
	var datas, ecs [][]byte
	off := 0
	for _, n := range blk.data {
		d := data[off : off+n]
		off += n
		datas = append(datas, d)
		ecs = append(ecs, rsRemainder(d, blk.ecPer))
	}
	var out []byte
	maxLen := blk.data[len(blk.data)-1]
	for i := 0; i < maxLen; i++ {
		for _, d := range datas {
			if i < len(d) {
				out = append(out, d[i])
			}
		}
	}
	for i := 0; i < blk.ecPer; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// GF(256) with the QR primitive polynomial 0x11D.
func gfMul(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= ((int(y) >> i) & 1) * int(x)
	}
	return byte(z)
}

func rsRemainder(data []byte, degree int) []byte {
	gen := make([]byte, degree)
	gen[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range gen {
			gen[j] = gfMul(gen[j], root)
			if j+1 < len(gen) {
				gen[j] ^= gen[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	res := make([]byte, degree)
	for _, b := range data {
		f := b ^ res[0]
		copy(res, res[1:])
		res[degree-1] = 0
		for i := range res {
			res[i] ^= gfMul(gen[i], f)
		}
	}
	return res
}

type builder struct {
	size, version int
	mod, fn       [][]bool
}

func newBuilder(size, version int) *builder {
	b := &builder{size: size, version: version}
	b.mod = make([][]bool, size)
	b.fn = make([][]bool, size)
	for i := range b.mod {
		b.mod[i] = make([]bool, size)
		b.fn[i] = make([]bool, size)
	}
	return b
}

func (b *builder) set(x, y int, dark bool) {
	b.mod[y][x] = dark
	b.fn[y][x] = true
}

func (b *builder) drawFunctionPatterns() {
	for i := 0; i < b.size; i++ {
		b.set(6, i, i%2 == 0)
		b.set(i, 6, i%2 == 0)
	}
	for _, p := range [][2]int{{3, 3}, {b.size - 4, 3}, {3, b.size - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := p[0]+dx, p[1]+dy
				if x < 0 || y < 0 || x >= b.size || y >= b.size {
					continue
				}
				d := max(abs(dx), abs(dy))
				b.set(x, y, d != 2 && d != 4)
			}
		}
	}
	pos := alignPos[b.version]
	for i, cy := range pos {
		for j, cx := range pos {
			if (i == 0 && j == 0) || (i == 0 && j == len(pos)-1) || (i == len(pos)-1 && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					b.set(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	b.drawFormat(0) // reserve
	if b.version >= 7 {
		rem := b.version
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		bits := b.version<<12 | rem
		for i := 0; i < 18; i++ {
			dark := (bits>>i)&1 == 1
			a, c := b.size-11+i%3, i/3
			b.set(a, c, dark)
			b.set(c, a, dark)
		}
	}
	b.set(8, b.size-8, true) // dark module
}

func (b *builder) drawFormat(mask int) {
	data := 0b01<<3 | mask // level L = 01
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		b.set(8, i, bit(i))
	}
	b.set(8, 7, bit(6))
	b.set(8, 8, bit(7))
	b.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		b.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		b.set(b.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		b.set(8, b.size-15+i, bit(i))
	}
	b.set(8, b.size-8, true)
}

func (b *builder) drawData(cw []byte) {
	i := 0
	for right := b.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < b.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if ((right + 1) & 2) == 0 {
					y = b.size - 1 - vert
				}
				if !b.fn[y][x] && i < len(cw)*8 {
					b.mod[y][x] = (cw[i/8]>>(7-i%8))&1 == 1
					i++
				}
			}
		}
	}
}

func (b *builder) applyMask(m int) {
	for y := 0; y < b.size; y++ {
		for x := 0; x < b.size; x++ {
			var inv bool
			switch m {
			case 0:
				inv = (x+y)%2 == 0
			case 1:
				inv = y%2 == 0
			case 2:
				inv = x%3 == 0
			case 3:
				inv = (x+y)%3 == 0
			case 4:
				inv = (x/3+y/2)%2 == 0
			case 5:
				inv = x*y%2+x*y%3 == 0
			case 6:
				inv = (x*y%2+x*y%3)%2 == 0
			case 7:
				inv = ((x+y)%2+x*y%3)%2 == 0
			}
			if inv && !b.fn[y][x] {
				b.mod[y][x] = !b.mod[y][x]
			}
		}
	}
}

func (b *builder) penalty() int {
	n := b.size
	score := 0
	line := func(get func(i int) bool) {
		run := 1
		for i := 1; i < n; i++ {
			if get(i) == get(i-1) {
				run++
				continue
			}
			if run >= 5 {
				score += run - 2
			}
			run = 1
		}
		if run >= 5 {
			score += run - 2
		}
	}
	for k := 0; k < n; k++ {
		line(func(i int) bool { return b.mod[k][i] })
		line(func(i int) bool { return b.mod[i][k] })
	}
	for y := 0; y < n-1; y++ {
		for x := 0; x < n-1; x++ {
			c := b.mod[y][x]
			if c == b.mod[y][x+1] && c == b.mod[y+1][x] && c == b.mod[y+1][x+1] {
				score += 3
			}
		}
	}
	dark := 0
	for y := range b.mod {
		for _, c := range b.mod[y] {
			if c {
				dark++
			}
		}
	}
	k := abs(dark*20-n*n*10) / (n * n) // each step is 5% off 50%
	score += k * 10
	return score
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// Terminal renders the code with half-block characters (two rows per text
// line) inside a 2-module quiet zone. Each line forces black-on-white
// (ANSI 47;30) so the polarity phone scanners expect holds on dark terminal
// themes too.
func (c *Code) Terminal() string {
	const q = 2
	var sb strings.Builder
	for y := -q; y < c.Size+q; y += 2 {
		sb.WriteString("\x1b[47;30m")
		for x := -q; x < c.Size+q; x++ {
			top, bot := c.Dark(x, y), c.Dark(x, y+1)
			switch {
			case top && bot:
				sb.WriteString("█")
			case top:
				sb.WriteString("▀")
			case bot:
				sb.WriteString("▄")
			default:
				sb.WriteString(" ")
			}
		}
		sb.WriteString("\x1b[0m\n")
	}
	return sb.String()
}
