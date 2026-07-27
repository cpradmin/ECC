package handlers

import "github.com/cpradmin/prompts-mcp/models"

// deepCopyIndex creates a deep copy of a RegistryIndex to prevent races on interior pointers
func (rh *RegistryHandler) deepCopyIndex(index *models.RegistryIndex) *models.RegistryIndex {
	if index == nil {
		return nil
	}
	copy := &models.RegistryIndex{
		Version:      index.Version,
		GeneratedAt:  index.GeneratedAt,
		TotalPrompts: index.TotalPrompts,
		Domains:      make(map[string]models.DomainStats),
		Prompts:      make([]models.RegistryEntry, len(index.Prompts)),
	}
	// Copy domain stats
	for k, v := range index.Domains {
		copy.Domains[k] = v
	}
	// Copy prompts
	for i, entry := range index.Prompts {
		copy.Prompts[i] = models.RegistryEntry{
			ID:             entry.ID,
			Domain:         entry.Domain,
			CurrentVersion: entry.CurrentVersion,
			Confidence:     entry.Confidence,
			SuccessRate:    entry.SuccessRate,
			AgentsTested:   append([]string{}, entry.AgentsTested...),
			CreatedAt:      entry.CreatedAt,
			UpdatedAt:      entry.UpdatedAt,
			Versions:       append([]string{}, entry.Versions...),
			RegistryStatus: entry.RegistryStatus,
			Promoted:       entry.Promoted,
		}
	}
	return copy
}
