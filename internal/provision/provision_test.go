package provision

import "testing"

const versionOut = `
          release: 5.01.C.3.0-1
            title: 5.1.3
             arch: mips
           components: acl,base,wireguard,
                       wireguard-server
      description: Keenetic Giga (KN-1011)
            model: Giga (KN-1011)
`

func TestParseKeeneticVersion(t *testing.T) {
	info := parseKeeneticVersion(versionOut)
	if info.title != "5.1.3" {
		t.Fatalf("title = %q", info.title)
	}
	if info.description != "Keenetic Giga (KN-1011)" {
		t.Fatalf("description = %q", info.description)
	}
	if info.major != 5 || info.minor != 1 {
		t.Fatalf("gate version = %d.%d", info.major, info.minor)
	}
	if !info.components["wireguard"] {
		t.Fatal("wireguard component not parsed")
	}
}
