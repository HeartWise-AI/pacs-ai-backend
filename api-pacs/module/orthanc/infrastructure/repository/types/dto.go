package types

type UpsertDICOMModality struct {
	TenantID      string
	ModalityID    string
	AET           string
	HostHash      string
	CFindEnabled  bool
	CMoveEnabled  bool
	CStoreEnabled bool
}
