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

type GetDICOMModalityResult struct {
	TargetCFindEnabled  bool
	TargetCMoveEnabled  bool
	TargetCStoreEnabled bool
}
