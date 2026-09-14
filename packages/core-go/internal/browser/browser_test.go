package browser

import "testing"

func TestBuildOpenArgv(t *testing.T) {
	old := goos
	defer func() { goos = old }()

	cases := []struct {
		platform string
		wantName string
	}{
		{"darwin", "open"},
		{"windows", "rundll32"},
		{"linux", "xdg-open"},
	}
	for _, c := range cases {
		goos = c.platform
		name, args, err := buildOpenArgv("http://localhost:3001/")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.platform, err)
		}
		if name != c.wantName {
			t.Errorf("%s: expected command %q, got %q", c.platform, c.wantName, name)
		}
		found := false
		for _, a := range args {
			if a == "http://localhost:3001/" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected the URL to appear in args %v", c.platform, args)
		}
	}
}

func TestBuildOpenArgv_UnsupportedPlatform(t *testing.T) {
	old := goos
	defer func() { goos = old }()
	goos = "plan9"

	if _, _, err := buildOpenArgv("http://localhost:3001/"); err == nil {
		t.Fatal("expected an error for an unsupported platform")
	}
}
