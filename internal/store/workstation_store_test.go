package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// jumpHostFixture is the exact metadata JSON the web UI payload builder emits for a
// workstation with a jump host (see workstation-contract.test.ts).
const jumpHostFixture = `{
  "host": "10.0.0.5",
  "port": 22,
  "user": "ubuntu",
  "privateKey": "KEY",
  "jumpHost": {
    "host": "bastion.example.com",
    "port": 2222,
    "user": "jumper",
    "password": "s3cret",
    "knownHostsFingerprint": "SHA256:abcdef"
  }
}`

func TestUnmarshalSSHMetadata_JumpHost(t *testing.T) {
	m, err := UnmarshalSSHMetadata([]byte(jumpHostFixture))
	if err != nil {
		t.Fatalf("valid jumpHost rejected: %v", err)
	}
	if m.JumpHost == nil || m.JumpHost.Host != "bastion.example.com" || m.JumpHost.Port != 2222 {
		t.Fatalf("jumpHost not parsed: %+v", m.JumpHost)
	}
}

func TestUnmarshalSSHMetadata_LegacyWithoutJumpHost(t *testing.T) {
	m, err := UnmarshalSSHMetadata([]byte(`{"host":"10.0.0.5","user":"ubuntu","password":"pw"}`))
	if err != nil {
		t.Fatalf("legacy metadata rejected: %v", err)
	}
	if m.JumpHost != nil || m.Port != 22 {
		t.Fatalf("legacy metadata changed shape: %+v", m)
	}
}

func TestUnmarshalSSHMetadata_JumpHostInvalid(t *testing.T) {
	jump := func(body string) string {
		return `{"host":"10.0.0.5","user":"u","password":"pw","jumpHost":` + body + `}`
	}
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"missing host", jump(`{"user":"j","password":"p","knownHostsFingerprint":"SHA256:a"}`), "jumpHost: host is required"},
		{"missing user", jump(`{"host":"b","password":"p","knownHostsFingerprint":"SHA256:a"}`), "jumpHost: user is required"},
		{"bad port", jump(`{"host":"b","user":"j","port":70000,"password":"p","knownHostsFingerprint":"SHA256:a"}`), "jumpHost: port 70000 out of range"},
		{"no credential", jump(`{"host":"b","user":"j","knownHostsFingerprint":"SHA256:a"}`), "jumpHost: privateKey or password is required"},
		{"no fingerprint (TOFU refused)", jump(`{"host":"b","user":"j","password":"p"}`), "jumpHost: knownHostsFingerprint is required"},
		{"nested jump rejected", jump(`{"host":"b","user":"j","password":"p","knownHostsFingerprint":"SHA256:a","jumpHost":{"host":"c"}}`), "jumpHost:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalSSHMetadata([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestUnmarshalSSHMetadata_SSRFLiteralHosts(t *testing.T) {
	build := func(host, jumpHost string) string {
		raw := `{"host":"` + host + `","user":"u","password":"pw"`
		if jumpHost != "" {
			raw += `,"jumpHost":{"host":"` + jumpHost + `","user":"j","password":"p","knownHostsFingerprint":"SHA256:a"}`
		}
		return raw + "}"
	}
	blocked := []string{"169.254.169.254", "fe80::1", "224.0.0.1", "0.0.0.0"}
	allowed := []string{"127.0.0.1", "10.0.0.1", "::1", "172.16.0.5", "192.168.1.10", "example.com"}

	for _, h := range blocked {
		if _, err := UnmarshalSSHMetadata([]byte(build(h, ""))); err == nil || !strings.Contains(err.Error(), "host "+h) {
			t.Errorf("target host %s: err = %v, want SSRF rejection", h, err)
		}
		if _, err := UnmarshalSSHMetadata([]byte(build("10.0.0.5", h))); err == nil || !strings.Contains(err.Error(), "jumpHost.host "+h) {
			t.Errorf("jump host %s: err = %v, want SSRF rejection", h, err)
		}
	}
	for _, h := range allowed {
		if _, err := UnmarshalSSHMetadata([]byte(build(h, ""))); err != nil {
			t.Errorf("target host %s rejected: %v", h, err)
		}
		if _, err := UnmarshalSSHMetadata([]byte(build("10.0.0.5", h))); err != nil {
			t.Errorf("jump host %s rejected: %v", h, err)
		}
	}
}

func TestSanitizedView_JumpHostSummaryHasNoSecrets(t *testing.T) {
	ws := &Workstation{BackendType: BackendSSH, Metadata: []byte(jumpHostFixture)}
	view := ws.SanitizedView()

	jh, ok := view.MetadataSummary["jumpHost"].(map[string]any)
	if !ok {
		t.Fatalf("jumpHost summary missing: %+v", view.MetadataSummary)
	}
	if jh["host"] != "bastion.example.com" || jh["port"] != 2222 || jh["user"] != "jumper" || jh["hasKey"] != false {
		t.Fatalf("unexpected jumpHost summary: %+v", jh)
	}

	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"s3cret", "SHA256:abcdef", "KEY"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("sanitized view leaked %q: %s", secret, body)
		}
	}
}
