package auth

// CodespaceToken returns the GitHub token a Codespace gives its user, or "" outside of one.
// For players it can only clone the repo the Codespace was made from, and GitHub replaces it
// on every restart. aocn hands it to the API once to sign in, like the device flow's token,
// so players in a Codespace never confirm a code. getenv is os.Getenv outside of tests.
func CodespaceToken(getenv func(string) string) string {
	if getenv("CODESPACES") != "true" {
		return ""
	}
	return getenv("GITHUB_TOKEN")
}
