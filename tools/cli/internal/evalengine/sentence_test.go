package evalengine

import "testing"

func TestSplitSentences(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []sentenceSeg
	}{
		{"empty", "", nil},
		{
			"single unterminated",
			"bar bar",
			[]sentenceSeg{{0, 7, 7}},
		},
		{
			"single terminated",
			"foo foo.",
			[]sentenceSeg{{0, 8, 8}},
		},
		{
			"two sentences, second unterminated",
			"foo foo. bar bar",
			[]sentenceSeg{{0, 8, 9}, {9, 16, 16}},
		},
		{
			"three sentences all terminated",
			"foo foo. bar bar. baz baz.",
			[]sentenceSeg{{0, 8, 9}, {9, 17, 18}, {18, 26, 26}},
		},
		{
			"exclamation and question boundaries",
			"hi there! how are you? fine",
			[]sentenceSeg{{0, 9, 10}, {10, 22, 23}, {23, 27, 27}},
		},
		{
			"hard newline is a boundary with no punctuation",
			"line one\nline two",
			[]sentenceSeg{{0, 8, 9}, {9, 17, 17}},
		},
		{
			"trailing whitespace after terminal punctuation is absorbed",
			"foo foo.   ",
			[]sentenceSeg{{0, 8, 11}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitSentences(c.text)
			if len(got) != len(c.want) {
				t.Fatalf("text %q: got %d segments %+v, want %d %+v", c.text, len(got), got, len(c.want), c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("text %q: segment #%d = %+v, want %+v", c.text, i, got[i], c.want[i])
				}
			}
		})
	}
}
