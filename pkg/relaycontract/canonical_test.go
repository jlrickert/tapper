package relaycontract_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

func TestCanonicalModel(t *testing.T) {
	cases := map[string]string{
		"gemma4:31b-mlx":                      "gemma4-31b",
		"gemma4-31b":                          "gemma4-31b",
		"qwen3.6:35b-mlx":                     "qwen3.6-35b",
		"qwen/qwen3.6-35b":                    "qwen3.6-35b",
		"qwen3-coder-next:latest":             "qwen3-coder-next",
		"glm-ocr:latest":                      "glm-ocr",
		"hf.co/unsloth/Qwen3-32B-GGUF:Q4_K_M": "qwen3-32b",
		"llama3.1:8b-instruct-q4_K_M":         "llama3.1-8b-instruct",
		"mlx-community/Qwen3-8B-4bit":         "qwen3-8b",
		"gpt-oss:20b":                         "gpt-oss-20b",
		"openai/gpt-oss-120b-mxfp4":           "gpt-oss-120b",
		"mlx":                                 "mlx",
		"qwen3.6:35b-a3b-coding-mxfp8":        "qwen3.6-35b-a3b-coding",
		"qwen3.6:35b-a3b-coding-nvfp4":        "qwen3.6-35b-a3b-coding",
		"qwen3.6:35b-a3b-mlx-bf16":            "qwen3.6-35b-a3b",
		"qwen3.6:35b-a3b-mxfp8":               "qwen3.6-35b-a3b",
		"qwen3.6:35b-a3b-nvfp4":               "qwen3.6-35b-a3b",
		"qwen3.8:27b-mlx":                     "qwen3.8-27b",
		"  Phi4  ":                            "phi4",
	}
	for in, want := range cases {
		got := relaycontract.CanonicalModel(in)
		if got != want {
			t.Errorf("CanonicalModel(%q) = %q, want %q", in, got, want)
		}
		if !relaycontract.ValidCanonical(got) {
			t.Errorf("CanonicalModel(%q) = %q is not valid", in, got)
		}
	}
}

func TestValidCanonical(t *testing.T) {
	for _, bad := range []string{"", "-x", "Qwen3", "a/b", "a:b", string(make([]byte, 129))} {
		if relaycontract.ValidCanonical(bad) {
			t.Errorf("ValidCanonical(%q) = true", bad)
		}
	}
}
