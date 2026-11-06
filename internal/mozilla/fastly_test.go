package mozilla

import "testing"

// The challenge script's init call comes in two shapes — the legacy
// one-liner and the newer multi-line call with comments — and both must
// yield the challenge list and the token.
func TestParseChallengeInit(t *testing.T) {
	const prefix = "p.m1.fastly-masque.net/_fs-ch-AbC123"
	const challenges = `[{"ty":"pow","data":{"base":"abc","hash":"deadbeef","hmac":"hm","expires":"ex"}}]`
	scripts := map[string]string{
		"legacy": `var a=1; init(` + challenges + `, "token-1", "` + prefix + `");`,
		"new": "var noise={init:function(){}};something.init([1,2]);\ninit(\n  " + challenges + ",\n  \"token-2\",\n  \"" + prefix + "\",\n  true // trailing comment\n);",
	}
	for name, script := range scripts {
		chs, token, err := parseChallengeInit(script, prefix)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if token == "" {
			t.Fatalf("%s: empty token", name)
		}
		if len(chs) != 1 || chs[0]["ty"] != "pow" {
			t.Fatalf("%s: wrong challenges %+v", name, chs)
		}
	}
	if _, _, err := parseChallengeInit("no init here at all", prefix); err == nil {
		t.Fatal("a script without an init call must fail")
	}
}
