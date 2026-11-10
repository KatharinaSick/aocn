package app

import "testing"

func fullConfig() Config {
	var c Config
	c.Database.URL = "postgres://localhost/aocn"
	c.Resend.APIKey = "re_test"
	c.Resend.SegmentID = "seg_1"
	c.Subscribe.SigningKey = "key"
	c.Doors.Bucket = "doors"
	return c
}

// Without dev mode, a missing secret must stop the API, wherever it runs: falling back
// would accept signups that never send an email.
func TestValidateRequiresSecretsOutsideDevMode(t *testing.T) {
	cases := map[string]func(*Config){
		"no api key":     func(c *Config) { c.Resend.APIKey = "" },
		"no segment":     func(c *Config) { c.Resend.SegmentID = "" },
		"no signing key": func(c *Config) { c.Subscribe.SigningKey = "" },
		"no database":    func(c *Config) { c.Database.URL = "" },
		"no doors":       func(c *Config) { c.Doors.Bucket = "" },
	}
	for name, change := range cases {
		for _, onCloudRun := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				c := fullConfig()
				change(&c)
				if err := c.validate(onCloudRun); err == nil {
					t.Errorf("onCloudRun=%v: no error", onCloudRun)
				}
			})
		}
	}

	c := fullConfig()
	if err := c.validate(true); err != nil {
		t.Errorf("complete config: %v", err)
	}
}

func TestValidateDevMode(t *testing.T) {
	var empty Config
	empty.Dev = true
	if err := empty.validate(false); err == nil {
		t.Error("dev mode without a database accepted")
	}

	empty.Database.URL = "postgres://localhost/aocn"
	empty.Doors.Dir = "../build"
	if err := empty.validate(false); err != nil {
		t.Errorf("dev mode without secrets: %v", err)
	}

	if err := empty.validate(true); err == nil {
		t.Error("dev mode accepted on Cloud Run")
	}

	keyOnly := empty
	keyOnly.Resend.APIKey = "re_test"
	if err := keyOnly.validate(false); err == nil {
		t.Error("dev mode with an API key but no segment accepted")
	}
}

func TestValidateDoors(t *testing.T) {
	both := fullConfig()
	both.Doors.Dir = "../build"
	if err := both.validate(false); err == nil {
		t.Error("bucket and dir both set accepted")
	}

	dir := fullConfig()
	dir.Doors.Bucket = ""
	dir.Doors.Dir = "../build"
	if err := dir.validate(false); err != nil {
		t.Errorf("dir outside Cloud Run: %v", err)
	}
	if err := dir.validate(true); err == nil {
		t.Error("dir accepted on Cloud Run")
	}
}

func TestParsePreviewers(t *testing.T) {
	p, err := ParsePreviewers(" 9420018:all\n7654321:1  1111111:3,5 ")
	if err != nil {
		t.Fatal(err)
	}
	may := map[struct {
		id   int64
		door int
	}]bool{
		{9420018, 1}: true, {9420018, 24}: true,
		{7654321, 1}: true, {7654321, 2}: false, {7654321, 3}: false,
		{1111111, 3}: true, {1111111, 5}: true, {1111111, 1}: false, {1111111, 4}: false,
		{2222222, 1}: false,
	}
	for c, want := range may {
		if got := p.May(c.id, c.door); got != want {
			t.Errorf("May(%d, %d) = %v, want %v", c.id, c.door, got, want)
		}
	}

	if empty, err := ParsePreviewers(""); err != nil || empty.May(9420018, 1) {
		t.Errorf("empty: %v, %v", empty, err)
	}
}

// Anything unclear stops the API instead of quietly giving someone the wrong doors.
func TestParsePreviewersRefuses(t *testing.T) {
	for _, s := range []string{
		"9420018",           // no doors
		"9420018:",          // no doors
		"katharinasick:all", // a login
		"9420018:one",       // not a door
		"9420018:0",         // no door 0
		"9420018:1,,2",      // empty door
		"-5:all", "0:all",   // not an ID
		"9420018:1 9420018:3", // twice
		"9420018:1,7654321:2", // entries are separated by spaces
	} {
		if got, err := ParsePreviewers(s); err == nil {
			t.Errorf("%q: got %v, want an error", s, got)
		}
	}
}
