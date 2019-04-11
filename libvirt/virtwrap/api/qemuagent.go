package api

// Result for json unmarshalling
type GuestNetworkInterface struct {
	Interfaces []GuestInterface `json:"return"`
}

// Interface for json unmarshalling
type GuestInterface struct {
	MAC  string    `json:"hardware-address"`
	IPs  []GuestIP `json:"ip-addresses"`
	Name string    `json:"name"`
}

// IP for json unmarshalling
type GuestIP struct {
	IP     string `json:"ip-address"`
	Type   string `json:"ip-address-type"`
	Prefix int    `json:"prefix"`
}
