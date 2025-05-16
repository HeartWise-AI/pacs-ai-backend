package types

type UpsertDICOMModality struct {
	ID            string
	TenantID      string
	AET           string
	HostHash      string
	CFindEnabled  bool
	CMoveEnabled  bool
	CStoreEnabled bool
}
