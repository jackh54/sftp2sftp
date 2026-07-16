package endpoint

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Endpoint
	}{
		{
			in: "user@host1:2022/path/to/server",
			want: Endpoint{User: "user", Host: "host1", Port: 2022, Path: "/path/to/server"},
		},
		{
			in: "root@example.com/var/www",
			want: Endpoint{User: "root", Host: "example.com", Port: 22, Path: "/var/www"},
		},
		{
			in: "mc@[2001:db8::1]:2222/home/mc/server",
			want: Endpoint{User: "mc", Host: "2001:db8::1", Port: 2222, Path: "/home/mc/server"},
		},
	}

	for _, tc := range tests {
		got, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "host/path", "user@host", "user@host:abc/path"} {
		if _, err := Parse(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}
