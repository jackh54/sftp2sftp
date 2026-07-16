package endpoint

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Endpoint
	}{
		{
			in:   "sftp://user.abcd1234@node.example.com:2022",
			want: Endpoint{User: "user.abcd1234", Host: "node.example.com", Port: 2022, Path: "/"},
		},
		{
			in:   "sftp://user@host1:2022/path/to/server",
			want: Endpoint{User: "user", Host: "host1", Port: 2022, Path: "/path/to/server"},
		},
		{
			in:   "sftp://root@example.com/var/www",
			want: Endpoint{User: "root", Host: "example.com", Port: 22, Path: "/var/www"},
		},
		{
			in:   "sftp://mc@[2001:db8::1]:2222/home/mc/server",
			want: Endpoint{User: "mc", Host: "2001:db8::1", Port: 2222, Path: "/home/mc/server"},
		},
		{
			in:   "sftp://user@[2001:db8::1]:2022",
			want: Endpoint{User: "user", Host: "2001:db8::1", Port: 2022, Path: "/"},
		},
		// Legacy form still accepted.
		{
			in:   "user@host1:2022/path/to/server",
			want: Endpoint{User: "user", Host: "host1", Port: 2022, Path: "/path/to/server"},
		},
		{
			in:   "root@example.com/var/www",
			want: Endpoint{User: "root", Host: "example.com", Port: 22, Path: "/var/www"},
		},
		{
			in:   "mc@[2001:db8::1]:2222/home/mc/server",
			want: Endpoint{User: "mc", Host: "2001:db8::1", Port: 2222, Path: "/home/mc/server"},
		},
		{
			in:   "user.abcd1234@node.example.com:2022",
			want: Endpoint{User: "user.abcd1234", Host: "node.example.com", Port: 2022, Path: "/"},
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

func TestString(t *testing.T) {
	tests := []struct {
		ep   Endpoint
		want string
	}{
		{
			ep:   Endpoint{User: "user.abcd1234", Host: "node.example.com", Port: 2022, Path: "/"},
			want: "sftp://user.abcd1234@node.example.com:2022",
		},
		{
			ep:   Endpoint{User: "root", Host: "example.com", Port: 22, Path: "/var/www"},
			want: "sftp://root@example.com/var/www",
		},
		{
			ep:   Endpoint{User: "mc", Host: "2001:db8::1", Port: 2222, Path: "/home/mc/server"},
			want: "sftp://mc@[2001:db8::1]:2222/home/mc/server",
		},
	}

	for _, tc := range tests {
		if got := tc.ep.String(); got != tc.want {
			t.Fatalf("String() = %q, want %q", got, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		"",
		"host/path",
		"user@host:abc/path",
		"http://user@host:22",
		"sftp://@host:2022",
		"sftp://user@:2022",
		"sftp://user:secret@host:2022",
		"sftp://user@host:2022?x=1",
		"sftp://user@host:2022#frag",
	} {
		if _, err := Parse(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}
