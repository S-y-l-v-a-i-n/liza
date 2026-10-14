package paths

// AdmissionSlotsDir returns the branded runtime directory holding, per
// admission-limit class, the slot locks and waiter tickets (ADR-0209).
func (p LizaPaths) AdmissionSlotsDir() string {
	return p.get("admission-slots")
}
