package types

type AddContactForm struct {
	Token   string
	Name    string
	Email   string
	Message string
}

type Subscribe struct {
	Token string
	Email string
}
