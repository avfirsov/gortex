package astquery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJSRulesCompile drives each JS+TS rule against a stub so a
// tree-sitter pattern compile error surfaces at unit-test time.
func TestJSRulesCompile(t *testing.T) {
	const stub = "var x = 1;\n"
	for _, info := range DescribeDetectors() {
		if info.Category != CategorySAST {
			continue
		}
		hasJS := false
		for _, l := range info.Languages {
			if l == "javascript" {
				hasJS = true
				break
			}
		}
		if !hasJS {
			continue
		}
		t.Run(info.Name, func(t *testing.T) {
			_, err := RunOnSource(context.Background(), Options{Detector: info.Name},
				"sample.js", "javascript", []byte(stub))
			require.NoError(t, err, "rule %s pattern failed to compile", info.Name)
		})
	}
}

func TestJSRulesFire(t *testing.T) {
	cases := []struct {
		name string
		lang string
		bad  string
		good string
	}{
		{"js-eval-use", "javascript",
			`var y = eval("1+1");`,
			`var y = 2;`},
		{"js-dom-innerhtml-assignment", "javascript",
			`el.innerHTML = user;`,
			`el.textContent = user;`},
		{"js-document-write", "javascript",
			`document.write("<b>" + user + "</b>");`,
			`el.textContent = user;`},
		{"js-child-process-exec", "javascript",
			`var cp = require("child_process"); cp.exec(cmd);`,
			`var cp = require("child_process"); cp.execFile("ls", ["-la"]);`},
		{"js-require-with-variable", "javascript",
			`var m = require(mod);`,
			`var m = require("./fixed");`},
		{"js-cookie-no-secure-or-httponly", "javascript",
			`res.cookie("sid", v, { secure: false, httpOnly: false });`,
			`res.cookie("sid", v, { secure: true, httpOnly: true });`},
		{"js-postmessage-wildcard-target", "javascript",
			`win.postMessage(data, "*");`,
			`win.postMessage(data, "https://example.com");`},
		{"js-crypto-weak-hash", "javascript",
			`crypto.createHash("md5").update(s).digest("hex");`,
			`crypto.createHash("sha256").update(s).digest("hex");`},
		{"js-math-random-for-token", "javascript",
			`var tok = Math.random();`,
			`var tok = crypto.randomUUID();`},
		{"js-no-tls-reject-unauthorized", "javascript",
			`process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";`,
			`// nothing`},
		{"js-jwt-none-alg", "javascript",
			`jwt.sign(payload, key, { algorithm: "none" });`,
			`jwt.sign(payload, key, { algorithm: "HS256" });`},
		{"js-location-href-assignment", "javascript",
			`window.location.href = userUrl;`,
			`window.location.assign("/fixed/path");`},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			bad := runDetector(t, c.name, c.lang, "case."+c.lang, c.bad)
			require.GreaterOrEqual(t, bad.Total, 1, "rule %q should fire on bad fixture; got 0", c.name)
			good := runDetector(t, c.name, c.lang, "case."+c.lang, c.good)
			require.Equal(t, 0, good.Total, "rule %q should be silent on good fixture; got %d", c.name, good.Total)
		})
	}
}
