package workgraph

import "testing"

func TestSystemOneEndpointResolvesEachHostShape(t *testing.T) {
	for baseURL, expected := range map[string]string{
		"http://127.0.0.1:8000":                                    "http://127.0.0.1:8000/v1/systemone",
		"https://api.typesafe.ai/":                                 "https://api.typesafe.ai/v1/systemone",
		"http://localhost:11434/v1":                                "http://localhost:11434/v1/systemone",
		"https://inference.example/v1/":                            "https://inference.example/v1/systemone",
		"https://foundry.example/providers/microsoft/v1/systemone": "https://foundry.example/providers/microsoft/v1/systemone",
		"https://api.cloudflare.com/client/v4/accounts/abc/ai/run/@cf/cloudflare/clef": "https://api.cloudflare.com/client/v4/accounts/abc/ai/run/@cf/cloudflare/clef",
	} {
		if got := systemOneEndpoint(baseURL); got != expected {
			t.Errorf("systemOneEndpoint(%q) = %q, want %q", baseURL, got, expected)
		}
	}
}

func TestSystemOneDestinationShowsOnlySchemeAndHost(t *testing.T) {
	if got := systemOneDestination("https://api.cloudflare.com/client/v4/accounts/abc/ai/run/@cf/cloudflare/clef"); got != "https://api.cloudflare.com" {
		t.Fatalf("expected only scheme and host, got %q", got)
	}
}
