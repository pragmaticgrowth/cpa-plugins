package core

import "testing"

func TestApplyConfigYAML_Defaults(t *testing.T) {
	if err := applyConfigYAML(nil); err != nil {
		t.Fatalf("nil config: %v", err)
	}
	c := CurrentConfig()
	if !c.UseWarpCredits || c.ModelPrefix != "warp/" || c.ClientVersion == "" {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestApplyConfigYAML_Override(t *testing.T) {
	err := applyConfigYAML([]byte("use_warp_credits: false\nmodel_prefix: \"w:\"\nclient_version: \"v9\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := CurrentConfig()
	if c.UseWarpCredits || c.ModelPrefix != "w:" || c.ClientVersion != "v9" {
		t.Fatalf("override failed: %+v", c)
	}
	// Restore defaults so later tests see a known config.
	_ = applyConfigYAML(nil)
}
