package wgconf

import "testing"

func TestRangeParams(t *testing.T) {
	data := []byte(`[Interface]
PrivateKey = SMAp//W+/qjES6qfuxaVCM8jQlQtHhgDgHZrhDCfe00=
Address = 10.200.0.2/32
Jc = 6
Jmin = 10
Jmax = 50
S1 = 56
S2 = 32
S3 = 24
S4 = 12
H1 = 24294-85973
H2 = 100485-179345
H3 = 201034-276124
H4 = 331092-380876
I1 = <b 0x16030101380100>
[Peer]
PublicKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Endpoint = 203.0.113.50:789
AllowedIPs = 0.0.0.0/0
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	args := cfg.AWG.AscArgs()
	want := []string{"6", "10", "50", "56", "32", "24294-85973", "100485-179345", "201034-276124", "331092-380876", "24", "12"}
	for i, w := range want {
		if args[i] != w {
			t.Fatalf("arg[%d] = %q, хочу %q; все: %v", i, args[i], w, args)
		}
	}
	if args[11] == `""` {
		t.Fatal("I1 потерялся")
	}
}
