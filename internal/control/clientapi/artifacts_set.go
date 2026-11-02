package clientapi

// SetArtifacts connects the retrieval route to a store. It exists because the
// artifact store is created after the API handler in the startup order (the
// Worker listener needs the same directory), and a deployment may start the API
// without one.
//
// Setting it is safe at any time before the first request; the field is read per
// request and never cached.
func (h *Handler) SetArtifacts(source ArtifactSource) {
	h.artifacts = source
}
