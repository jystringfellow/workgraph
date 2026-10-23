package workgraph

import "testing"

func TestSystemOneEndpointResolvesEachHostShape(t *testing.T) {
	for baseURL, expected := range map[string]string{
		"http://127.0.0.1:8000":                                                        "http://127.0.0.1:8000/v1/systemone",
		"https://api.typesafe.ai/":                                                     "https://api.typesafe.ai/v1/systemone",
		"http://localhost:11434/v1":                                                    "http://localhost:11434/v1/systemone",
		"https://inference.example/v1/":                                                "https://inference.example/v1/systemone",
		"https://inference.example/v1?api-version=2026-10-01":                          "https://inference.example/v1/systemone?api-version=2026-10-01",
		"https://inference.example?api-version=2026-10-01":                             "https://inference.example/v1/systemone?api-version=2026-10-01",
		"https://foundry.example/providers/microsoft/v1/systemone":                     "https://foundry.example/providers/microsoft/v1/systemone",
		"https://api.cloudflare.com/client/v4/accounts/abc/ai/run/@cf/cloudflare/clef": "https://api.cloudflare.com/client/v4/accounts/abc/ai/run/@cf/cloudflare/clef",
	} {
		got, err := systemOneEndpoint(baseURL)
		if err != nil || got != expected {
			t.Errorf("systemOneEndpoint(%q) = %q, %v; want %q", baseURL, got, err, expected)
		}
	}
}

func TestSystemOneEndpointRejectsFragmentsAndNonHTTPURLs(t *testing.T) {
	for _, baseURL := range []string{"https://inference.example/v1#frag", "https://inference.example/v1#", "ftp://inference.example", "inference.example/v1", "http://"} {
		if got, err := systemOneEndpoint(baseURL); err == nil {
			t.Errorf("systemOneEndpoint(%q) = %q, want an error", baseURL, got)
		}
	}
}

func TestSystemOneDestinationShowsOnlySchemeAndHost(t *testing.T) {
	for baseURL, expected := range map[string]string{
		"https://api.cloudflare.com/client/v4/accounts/abc/ai/run/@cf/cloudflare/clef": "https://api.cloudflare.com",
		"https://user:secret@decisions.example.net:8443/v1?token=abc":                  "https://decisions.example.net:8443",
	} {
		if got := systemOneDestination(baseURL); got != expected {
			t.Errorf("systemOneDestination(%q) = %q, want %q", baseURL, got, expected)
		}
	}
}

func TestRedactedLLMBaseURLDropsOnlySecrets(t *testing.T) {
	for baseURL, expected := range map[string]string{
		"https://user:secret@llm.example.net/v1?key=abc": "https://llm.example.net/v1?REDACTED",
		"http://localhost:11434/v1":                      "http://localhost:11434/v1",
	} {
		if got := redactedLLMBaseURL(baseURL); got != expected {
			t.Errorf("redactedLLMBaseURL(%q) = %q, want %q", baseURL, got, expected)
		}
	}
}
