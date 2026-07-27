package handlers

import "github.com/cpradmin/prompts-mcp/models"

// NewRegistryHandler creates a new registry handler
func NewRegistryHandler(dataHome string, loader *PromptLoader) *RegistryHandler {
	return &RegistryHandler{
		loader:  loader,
		builder: models.NewRegistryBuilder(dataHome),
	}
}

// OnChange registers a callback fired after the index is rebuilt. Used to
// invalidate the metrics snapshot so a rebuild is visible on the next scrape.
// Safe to call post-construction, but wiring must complete before serving to avoid races.
func (rh *RegistryHandler) OnChange(fn func()) {
	if fn != nil {
		rh.onChange = append(rh.onChange, fn)
	}
}

func (rh *RegistryHandler) notifyChange() {
	for _, fn := range rh.onChange {
		fn()
	}
}
