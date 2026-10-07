package links

type TLS struct {
	Security    string   `json:"security"`
	SNI         string   `json:"sni,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	ALPN        []string `json:"alpn,omitempty"`
	PublicKey   string   `json:"publicKey,omitempty"`
	ShortID     string   `json:"shortId,omitempty"`
}

type AWG struct {
	Jc   string `json:"jc,omitempty"`
	Jmin string `json:"jmin,omitempty"`
	Jmax string `json:"jmax,omitempty"`
	S1   string `json:"s1,omitempty"`
	S2   string `json:"s2,omitempty"`
	S3   string `json:"s3,omitempty"`
	S4   string `json:"s4,omitempty"`
	H1   string `json:"h1,omitempty"`
	H2   string `json:"h2,omitempty"`
	H3   string `json:"h3,omitempty"`
	H4   string `json:"h4,omitempty"`
	I1   string `json:"i1,omitempty"`
	I2   string `json:"i2,omitempty"`
	I3   string `json:"i3,omitempty"`
	I4   string `json:"i4,omitempty"`
	I5   string `json:"i5,omitempty"`
}

type Node struct {
	Type         string   `json:"type"`
	Tag          string   `json:"tag"`
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	UUID         string   `json:"uuid,omitempty"`
	Password     string   `json:"password,omitempty"`
	Encryption   string   `json:"encryption,omitempty"`
	Flow         string   `json:"flow,omitempty"`
	Transport    string   `json:"transport,omitempty"`
	Path         string   `json:"path,omitempty"`
	HeaderHost   string   `json:"headerHost,omitempty"`
	ServiceName  string   `json:"serviceName,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	TLS          *TLS     `json:"tls,omitempty"`
	PrivateKey   string   `json:"privateKey,omitempty"`
	PublicKey    string   `json:"publicKey,omitempty"`
	PresharedKey string   `json:"presharedKey,omitempty"`
	Addresses    []string `json:"addresses,omitempty"`
	MTU          int      `json:"mtu,omitempty"`
	Keepalive    int      `json:"keepalive,omitempty"`
	AWG          *AWG     `json:"awg,omitempty"`
	Raw          string   `json:"raw,omitempty"`
}

type SubInfo struct {
	Userinfo            string  `json:"userinfo,omitempty"`
	Upload              int64   `json:"upload,omitempty"`
	Download            int64   `json:"download,omitempty"`
	Total               int64   `json:"total,omitempty"`
	Expire              int64   `json:"expire,omitempty"`
	UpdateIntervalHours float64 `json:"updateIntervalHours,omitempty"`
}

type Result struct {
	Source   string   `json:"source"`
	Nodes    []Node   `json:"nodes"`
	Warnings []string `json:"warnings,omitempty"`
	Sub      *SubInfo `json:"subscription,omitempty"`
}

func (r *Result) warn(msg string) {
	r.Warnings = append(r.Warnings, msg)
}
