package middleware

import "testing"

func TestRedactBody(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"option SMTPToken", `{"key":"SMTPToken","value":"smtp_secret_123"}`, `{"key":"***","value":"***"}`},
		{"option reversed", `{"value":"smtp_secret_123","key":"SMTPToken"}`, `{"value":"***","key":"***"}`},
		{"option interleaved", `{"key":"SMTPToken","desc":"mail","value":"sec"}`, `{"key":"***","desc":"mail","value":"***"}`},
		{"option non-sensitive", `{"key":"Theme","value":"default"}`, `{"key":"***","value":"default"}`},
		{"channel key", `{"name":"ch","key":"sk-abc123"}`, `{"name":"ch","key":"***"}`},
		{"channel key with value field", `{"name":"ch","key":"sk-123","value":"x"}`, `{"name":"ch","key":"***","value":"x"}`},
		{"token field", `{"token":"sk-xxx"}`, `{"token":"***"}`},
		{"form", `api_key=sk-abc&x=1`, `api_key=***&x=1`},
		{"token key", `{"key":"my-token","value":"t-abc"}`, `{"key":"***","value":"***"}`},
		// 转义引号场景：value 首段已被打码，残留尾部不包含敏感原文
		{"nested quote", `{"key":"SMTPToken","value":"a\"b\"c"}`, `{"key":"***","value":"***"b\"c"}`},
	}
	for _, c := range cases {
		got := redactBody([]byte(c.in))
		if got != c.want {
			t.Errorf("%s:\n  got  %q\n  want %q", c.name, got, c.want)
		} else {
			t.Logf("%s: OK -> %s", c.name, got)
		}
	}
}
