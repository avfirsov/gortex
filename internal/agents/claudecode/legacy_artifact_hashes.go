package claudecode

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zzet/gortex/internal/agents"
)

// These hashes are byte-for-byte fingerprints of the last legacy-vocabulary
// artifacts shipped before the compact public surface. They let upgrades
// replace only untouched Gortex files while preserving every customized copy.
var legacyGlobalSkillHashes = map[string]string{
	"gortex-add-test":               "ac7088f6146395d15e70b011e4150a1708614e03763618202cb65367f807018c",
	"gortex-architecture-review":    "01594df88d730aab382dd3b2ef5d99c4cb03ce62ed1e04e56ef02049325efdcd",
	"gortex-cli":                    "8c71c2290a1695fc2c2461575ff949f0d33a659fff2d291e728c8109fb81d16b",
	"gortex-co-change":              "3bdf72fac5521b25785d059b80d3b8e37ed42c0128469cb69d0ce92723ea236c",
	"gortex-cross-repo-usage":       "4039ab9879a065b6c203796a0a4f0a3c28994e50f1e15524cf678e5123d9641e",
	"gortex-dataflow-trace":         "4eb7f28c754758b5926d7cd2b4cad630fa60aa98d3fbb81f49369174b9abb100",
	"gortex-debug":                  "3af79a0b0be77d0660ca8f8cb0343500cff6743b6e9fcfddbc4d542afdffd167",
	"gortex-episode-replay":         "d120d0948085837bf7a1066d16e0250999eff8f0f1acc067b970e51c9fddd598",
	"gortex-explore":                "0edc3c1fbd82483b993f960f4947c07bbcb6c0f5d3d230fd459425e69e874b97",
	"gortex-extract-function":       "637c84c094f221b3841685c5165c2eebc34ef7ebd06a5af07c54c7c600467ccd",
	"gortex-fix-all":                "33fc9b35567033434354188704aba70f952b7950e3df6b512d4df0adf87f9089",
	"gortex-guide":                  "d7cba2d96a2bba7c96df17e2f410e8de140764ec24c466123621b34cbb2ed7c2",
	"gortex-impact":                 "2111222dc7fcd4227db773ccb5ac8dca45f867f3ad2fba88b260569d5237a1ed",
	"gortex-incident-investigation": "30572b4534c5aa17a17789b52e1a74ab142e0b81e78b3fb85af207d4835d69f3",
	"gortex-onboarding":             "8803580cfefdbf45bb3a87e760340134f41dc5378cb5881a33bc7bfc054a448a",
	"gortex-pr-review":              "f87a000a7776e4db83c20f62372659efb354a4bca5d800cdd520ba05c1316cbe",
	"gortex-pr-review-agent":        "2be09fca75a79f7574cb57c5990aa87e27d72845cb8888a47b25e3129123ebb0",
	"gortex-quality-audit":          "51424a58bf9bbe9769997524feff7d5db07878d7012e49e8a5fcb855484b311a",
	"gortex-refactor":               "5d72e85a53df7cade5776cf0499ea5a2cb5a61f93bb89121abb5e829e1648eb6",
	"gortex-rename":                 "6416d2ee5a1c8c6c4231c4e7bc2de92cd6723bd968b4d72969b446df3920f807",
	"gortex-safe-edit":              "ccfc21a7ffc81ef8407b97cd54509f18226a09ee46a0d8fa3acc4265422f935e",
}

var legacySlashCommandHashes = map[string]string{
	"gortex-add-test.md":               "5dc170d682bfd0e9023b1d4f906378aa9bd017d11d1891e12166bc39f231a6d8",
	"gortex-architecture-review.md":    "79e168a3bf9a7cc015a565b33b9e78befc96fc3ddf57f2de716caa5490154f6c",
	"gortex-co-change.md":              "95a0176b2d231295d0936aa7e2334ee55ffa6aeddbd728f920732145cda3b784",
	"gortex-cross-repo-usage.md":       "00bdcb9545f95d2bcc30f67a815760577610fca6947b4d90aa80c75386f6bdcd",
	"gortex-dataflow-trace.md":         "a6d4d3a2d19ac9d628b1b3821f40e85bd17608690d2e8275011c5c3d857b8547",
	"gortex-debug.md":                  "4236422d3c14d3f0faf57648e00aa32e7c98e0c00f68dd29dfaa7c5c3a84a70f",
	"gortex-episode-replay.md":         "3ff36a94763529100b7acb0ee24d1134eb33ebe6884d98ff4fbd424510d69e40",
	"gortex-explore.md":                "9154a6bbcfc60c198890f29355faea3d8cf398c6d574b9d8ada905121663f39c",
	"gortex-extract-function.md":       "aa0d3c453b5e2ed5ec8bc28ef83e2ed6983126b53c79bcecdba205d7ff8c4e08",
	"gortex-fix-all.md":                "a3dd6bfa6a8a14915e766685e051e319b4193ba8e52d74811af0cb7fa5ebb58b",
	"gortex-guide.md":                  "068a0f2e7911bf0404888235490c97baf3a6632e9ef8eced171b09543851f480",
	"gortex-impact.md":                 "4c02060ae0868363d27c9b4b42a6a54ac432ac39e1687d4c2b77d5e4d740ee9d",
	"gortex-incident-investigation.md": "7a5b5d2be17eb76477acefc9beb41752b07cfb6a15aaa50e427a500a4b956638",
	"gortex-onboarding.md":             "4f09948418b3754d03321a81201185f744b55bb850cd2d18a5ace11708c97fe6",
	"gortex-pr-review-agent.md":        "776a9af046c26556561f8b9e9f92ac5c4737507c6380d5c274507294e9f72d26",
	"gortex-pr-review.md":              "0954c788915b270646c9593b1a4d890411a408bf1a6eaf76b3e40d8eb05dcfe0",
	"gortex-quality-audit.md":          "bd6bdb90fa9466c836c95755724bd359a7aae51d6818d7941da3063c2eee197b",
	"gortex-refactor.md":               "313fdc2f7daf28891ce19a94164c71a6e00eae225d76143251f3794b4d1d7a34",
	"gortex-rename.md":                 "5700aa7b1bdaba861c8e7f5b59623f17a49935de6bd8203bf7142a58499cbcd3",
	"gortex-safe-edit.md":              "5e487527a55816d64c23cc0fa080e024af3a771ed68e96c28a16940693f0b91d",
}

var legacySubAgentHashes = map[string]string{
	"gortex-impact.md": "bcf2bebeee1a932d1896e14a8de8ae8892191aed8683e233ca1d8ffa5276dae5",
	"gortex-search.md": "40cb39e36419993b2d75a9a13f363542bf392168e2d299e201165da7459f715b",
}

func artifactHash(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func isShippedAgentArtifact(existing []byte, current, legacyHash string) bool {
	return string(existing) == current || (legacyHash != "" && artifactHash(existing) == legacyHash)
}

func writeAgentArtifact(w io.Writer, path, current, legacyHash string, opts agents.ApplyOpts) (agents.FileAction, error) {
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return agents.WriteIfNotExists(w, path, current, opts)
	}
	if err != nil {
		return agents.FileAction{}, fmt.Errorf("read %s: %w", path, err)
	}
	if string(existing) == current {
		return agents.FileAction{Path: path, Action: agents.ActionSkip, Reason: "unchanged"}, nil
	}
	if legacyHash != "" && artifactHash(existing) == legacyHash {
		return agents.WriteOwnedFile(w, path, current, opts)
	}
	logWarn(w, "keeping customised agent artifact %s", path)
	return agents.FileAction{Path: path, Action: agents.ActionSkip, Reason: "customised"}, nil
}
