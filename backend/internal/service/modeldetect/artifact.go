package modeldetect

// ExtractArtwork returns only a complete document supplied by the model.
// It never repairs or completes the generated source.
func ExtractArtwork(text string) (string, string) { return extractDocument(text) }
