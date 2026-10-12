package commands

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLivenessPagesDecodeKeepsAbsentFieldsAbsent(t *testing.T) {
	var resp livenessPagesResponse
	body := `{"connected":2,"lastRollCallId":"rc-1","clients":[{"clientId":"c1","lastSeen":"t","stale":true,"state":{"device":"d1","voiceArmed":false,"screenErrors":["offline"]}}]}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	p := resp.Clients[0]
	if armedCell(p) != "off" || !p.Stale || p.State.LoadAddress != "" || orDash(p.State.LoadAddress) != "-" {
		t.Fatalf("unexpected decode: %+v", p)
	}
	if strings.Join(p.State.ScreenErrors, "") != "offline" {
		t.Fatalf("errors lost: %+v", p.State)
	}
	if armedCell(livenessPage{}) != "-" {
		t.Fatal("unreported voice state must render as absent, not off")
	}
}
