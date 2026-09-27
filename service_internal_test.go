package workgraph

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCaptureServiceDefinitionsEscapePaths(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			s := captureService{home: `/tmp/my "state" $cash %n & data`, label: "workgraph-test", platform: platform}
			definition := captureServiceDefinition(s, `/tmp/my "binary" $cash %n`, "/tmp/user")
			if platform == "darwin" {
				decoder := xml.NewDecoder(strings.NewReader(definition))
				var got []string
				for {
					token, err := decoder.Token()
					if err != nil {
						if !errors.Is(err, io.EOF) {
							t.Fatal(err)
						}
						break
					}
					if start, ok := token.(xml.StartElement); ok && start.Name.Local == "string" {
						var value string
						if err := decoder.DecodeElement(&value, &start); err != nil {
							t.Fatal(err)
						}
						got = append(got, value)
					}
				}
				if !strings.Contains(strings.Join(got, "\n"), s.home) {
					t.Fatalf("home did not round trip: %v", got)
				}
			} else {
				for _, expected := range []string{`\"state\" $$cash %%n`, "Restart=on-failure", "RestartSec=10", "WantedBy=default.target"} {
					if !strings.Contains(definition, expected) {
						t.Fatalf("missing %q: %s", expected, definition)
					}
				}
			}
		})
	}
}
