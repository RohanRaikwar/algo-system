package model

import "testing"

func TestPositionCommandValidateRemove(t *testing.T) {
	base := PositionCommand{Strategy: "NIFTY50_SR", Side: "CALL", Action: PositionSetLevels}
	ok := []PositionCommand{}
	for _, f := range []func(*PositionCommand){
		func(c *PositionCommand) { c.IndexStop = -1 },
		func(c *PositionCommand) { c.IndexTarget = -1 },
		func(c *PositionCommand) { c.IndexTrail = -1 },
		func(c *PositionCommand) { c.IndexTrail = 2000 },
		func(c *PositionCommand) { c.FNOTarget = -1 },
	} {
		c := base
		f(&c)
		ok = append(ok, c)
	}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []PositionCommand{base}
	for _, f := range []func(*PositionCommand){
		func(c *PositionCommand) { c.IndexStop = -2 },
		func(c *PositionCommand) { c.IndexTrail = -5 },
		func(c *PositionCommand) { c.FNOSL = -1 },
	} {
		c := base
		f(&c)
		bad = append(bad, c)
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}
