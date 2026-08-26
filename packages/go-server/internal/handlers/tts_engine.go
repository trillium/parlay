package handlers

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// maxDaemonResponse bounds a single speak-daemon reply. The body is one
// utterance's WAV, base64-encoded — a minute of 24 kHz 16-bit mono is ~3 MB
// raw, ~4 MB encoded — so 32 MiB is generous for any real synth and still far
// below anything that threatens the process.
const maxDaemonResponse = 32 << 20

// TTSEngine is the interface for text-to-speech synthesis backends.
// Implementations can be swapped to support different TTS providers (speak daemon, API services, etc).
type TTSEngine interface {
	// Synth synthesizes speech from the given text with optional voice and speed parameters.
	// Returns the WAV audio bytes, duration in seconds, and the voice used, or an error.
	Synth(text string, voice string, speed float64) (wav []byte, seconds float64, usedVoice string, err error)
}

// SpeakDaemonEngine connects to the local speak daemon via Unix socket.
// The daemon expects length-prefixed JSON messages.
type SpeakDaemonEngine struct {
	socketPath string
	timeout    time.Duration
}

// NewSpeakDaemonEngine creates a new speak daemon engine.
func NewSpeakDaemonEngine(socketPath string, timeout time.Duration) *SpeakDaemonEngine {
	return &SpeakDaemonEngine{
		socketPath: socketPath,
		timeout:    timeout,
	}
}

// Synth implements TTSEngine.Synth for the speak daemon.
func (e *SpeakDaemonEngine) Synth(text string, voice string, speed float64) ([]byte, float64, string, error) {
	// Build the command payload
	payload := map[string]interface{}{
		"command": "synth",
		"text":    text,
		"caller":  "parlay",
	}
	if voice != "" {
		payload["voice"] = voice
	}
	if speed != 0 {
		payload["speed"] = speed
	}

	// Marshal to JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to marshal synth request: %w", err)
	}

	// Connect to daemon with timeout
	conn, err := net.DialTimeout("unix", e.socketPath, e.timeout)
	if err != nil {
		return nil, 0, "", fmt.Errorf("speak daemon unreachable: %w", err)
	}
	defer conn.Close()

	// Set read/write deadlines. This is the only thing bounding how long the
	// read loop below can block, so a failure to set it must not be ignored.
	if err := conn.SetDeadline(time.Now().Add(e.timeout)); err != nil {
		return nil, 0, "", fmt.Errorf("failed to set daemon deadline: %w", err)
	}

	// Write length-prefixed message (4 bytes BE length + JSON)
	lenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBytes, uint32(len(body)))

	if _, err := conn.Write(lenBytes); err != nil {
		return nil, 0, "", fmt.Errorf("failed to write to daemon: %w", err)
	}
	if _, err := conn.Write(body); err != nil {
		return nil, 0, "", fmt.Errorf("failed to write to daemon: %w", err)
	}

	// Read response. The length prefix is attacker-controlled from this
	// process's point of view — it is whatever is on the other end of the
	// socket — so it is bounded before it is trusted, and the accumulator is
	// bounded independently. Without both, a daemon (or anything that manages
	// to bind that socket path) declaring a 2 GiB body makes this loop grow
	// `chunks` until the deadline expires or the process is OOM-killed.
	var chunks []byte
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			chunks = append(chunks, buf[:n]...)
			if len(chunks) > maxDaemonResponse {
				return nil, 0, "", fmt.Errorf("daemon response exceeds %d bytes", maxDaemonResponse)
			}
		}
		if err != nil {
			// Check if we have enough data
			if len(chunks) < 4 {
				return nil, 0, "", fmt.Errorf("daemon returned incomplete response")
			}
			break
		}

		// Check if we have a complete message
		if len(chunks) >= 4 {
			msgLen := int(binary.BigEndian.Uint32(chunks[:4]))
			if msgLen > maxDaemonResponse {
				return nil, 0, "", fmt.Errorf("daemon declared a %d byte response, over the %d byte limit", msgLen, maxDaemonResponse)
			}
			if len(chunks) >= 4+msgLen {
				// We have the complete message
				break
			}
		}
	}

	if len(chunks) < 4 {
		return nil, 0, "", fmt.Errorf("daemon returned incomplete response")
	}

	msgLen := int(binary.BigEndian.Uint32(chunks[:4]))
	if msgLen > maxDaemonResponse {
		return nil, 0, "", fmt.Errorf("daemon declared a %d byte response, over the %d byte limit", msgLen, maxDaemonResponse)
	}
	if len(chunks) < 4+msgLen {
		return nil, 0, "", fmt.Errorf("daemon returned incomplete response")
	}

	// Parse response JSON
	var resp struct {
		Ok      bool    `json:"ok"`
		Error   string  `json:"error,omitempty"`
		WavB64  string  `json:"wav_b64,omitempty"`
		Seconds float64 `json:"seconds,omitempty"`
		Voice   string  `json:"voice,omitempty"`
	}
	if err := json.Unmarshal(chunks[4:4+msgLen], &resp); err != nil {
		return nil, 0, "", fmt.Errorf("bad daemon response: %w", err)
	}

	if !resp.Ok {
		if resp.Error != "" {
			return nil, 0, "", fmt.Errorf("synth failed: %s", resp.Error)
		}
		return nil, 0, "", fmt.Errorf("synth failed")
	}

	// Decode base64 WAV
	wav, err := base64.StdEncoding.DecodeString(resp.WavB64)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to decode wav: %w", err)
	}

	return wav, resp.Seconds, resp.Voice, nil
}
