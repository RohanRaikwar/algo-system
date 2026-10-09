package exitpolicy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"trading-systemv1/internal/model"
)

func TestShippedConfigFileMatchesDefaults(t *testing.T) {
	c, err := LoadConfig("../../config/exitpolicy.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, DefaultConfig()) {
		t.Fatalf("config/exitpolicy.json drifted from DefaultConfig:\n%+v\n%+v", c, DefaultConfig())
	}
}

func TestLoadConfigOverlaysFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	body := `{"strategies":{"NIFTY50_SR":{"theta":{"max_flat_min":45}},"OTHER":{"mode":"shadow"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	sr := c.Strategies["NIFTY50_SR"]
	if sr.Mode != ModeAct || sr.Theta.MaxFlatMin != 45 || sr.Theta.DecayPctOfGain != 25 {
		t.Fatalf("SR overlay lost defaults: %+v", sr)
	}
	if g := c.Strategies["OTHER"]; g.Mode != ModeShadow || g.Theta != DefaultThetaConfig() {
		t.Fatalf("new strategy must start from default theta: %+v", g)
	}
}

func TestLoadConfigKeepsUnlistedDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(path, []byte(`{"strategies":{"OTHER":{"mode":"shadow"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Strategies["NIFTY50_SR"]; !ok {
		t.Fatal("unlisted default strategy dropped")
	}
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	for name, body := range map[string]string{
		"bad mode":     `{"strategies":{"NIFTY50_SR":{"mode":"live"}}}`,
		"real order":   `{"strategies":{"` + model.RealOrderStrategy + `":{"mode":"act"}}}`,
		"zero confirm": `{"strategies":{"NIFTY50_SR":{"theta":{"confirm_min":0}}}}`,
		"bad json":     `{`,
		"no max flat":  `{"strategies":{"NIFTY50_SR":{"theta":{"max_flat_min":0}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestPlanFor(t *testing.T) {
	c := DefaultConfig()
	if p, ok := c.PlanFor("NIFTY50_SR"); !ok || p.Mode != ModeAct || len(p.Rules) != 1 {
		t.Fatalf("SR plan %+v ok=%v", p, ok)
	}
	if _, ok := c.PlanFor("OTHER"); ok {
		t.Fatal("unlisted strategy must have no plan")
	}
	c.Strategies[model.RealOrderStrategy] = StrategyConfig{Mode: ModeAct, Theta: DefaultThetaConfig()}
	if p, ok := c.PlanFor(model.RealOrderStrategy); ok && p.Mode == ModeAct {
		t.Fatal("real-order strategy must never act")
	}
}
