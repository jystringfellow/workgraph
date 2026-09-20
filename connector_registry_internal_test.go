package workgraph

import "testing"

func TestConnectorRegistryDefinitionsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range connectorRegistry {
		if definition.ID == "" || seen[definition.ID] {
			t.Fatalf("connector registry has an empty or duplicate id %q", definition.ID)
		}
		seen[definition.ID] = true
		if definition.EventSource == "" {
			t.Fatalf("connector %s has no event source", definition.ID)
		}
		if !definition.Direct && !definition.Bridged {
			t.Fatalf("connector %s supports no capture mode", definition.ID)
		}
		if definition.DefaultInterval == nil || definition.DefaultInterval() <= 0 {
			t.Fatalf("connector %s has no positive default interval", definition.ID)
		}
		if definition.Bridged && (definition.CaptureSemantics == "" || definition.ValidateBridge == nil) {
			t.Fatalf("bridgeable connector %s lacks capture semantics or scope validation", definition.ID)
		}
		if !definition.Bridged && (definition.CaptureSemantics != "" || definition.ValidateBridge != nil || len(definition.RequiredTools) != 0) {
			t.Fatalf("direct-only connector %s declares bridged behavior", definition.ID)
		}
		if definition.Bridged {
			if len(definition.RequiredTools) == 0 {
				t.Fatalf("bridgeable connector %s has no provider tool requirements", definition.ID)
			}
			operations := map[string]bool{}
			hasFetch := false
			hasIdentity := false
			for _, requirement := range definition.RequiredTools {
				if requirement.Operation == "" || requirement.Detail == "" || len(requirement.Capabilities) == 0 {
					t.Fatalf("connector %s has an incomplete provider tool requirement: %#v", definition.ID, requirement)
				}
				key := requirement.Operation + "\x00" + requirement.When
				if operations[key] {
					t.Fatalf("connector %s repeats provider tool requirement %s", definition.ID, requirement.Operation)
				}
				operations[key] = true
				for _, capability := range requirement.Capabilities {
					switch capability {
					case "fetch":
						hasFetch = true
					case "identity":
						hasIdentity = true
					default:
						t.Fatalf("connector %s has unknown provider capability %q", definition.ID, capability)
					}
				}
			}
			if !hasFetch || !hasIdentity {
				t.Fatalf("bridgeable connector %s must declare fetch and identity requirements", definition.ID)
			}
		}
	}
}
