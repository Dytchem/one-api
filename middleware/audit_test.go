package middleware

import "testing"

func TestRedactBody(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"option SMTPToken", `{"key":"SMTPToken","value":"smtp_secret_123"}`, `{"key":"SMTPToken","value":"***"}`},
		{"option reversed", `{"value":"smtp_secret_123","key":"SMTPToken"}`, `{"value":"***","key":"SMTPToken"}`},
		{"option interleaved", `{"key":"SMTPToken","desc":"mail","value":"sec"}`, `{"key":"SMTPToken","desc":"mail","value":"***"}`},
		{"option non-sensitive", `{"key":"Theme","value":"default"}`, `{"key":"Theme","value":"default"}`},
		{"channel key", `{"name":"ch","key":"sk-abc123"}`, `{"name":"ch","key":"***"}`},
		{"form", `api_key=sk-abc&x=1`, `api_key=***&x=1`},
		{"token key", `{"key":"my-token","value":"t-abc"}`, `{"key":"my-token","value":"***"}`},
	}
	for _, c := range cases {
		got := redactBody([]byte(c.in))
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		} else {
			t.Logf("%s: OK -> %s", c.name, got)
		}
	}
}
