package entity

// Inference holds the inference entity fields
type Inference struct {
}

// GetModelName returns the model name of inference entity that can be used for naming schemas
func (entity *Inference) GetModelName() string {
	return "inferences"
}
