//go:build !windows

package processinfo

func ParentChatGPTExecutable() (string, bool) { return "", false }
func ParentChatGPTUserData() (string, bool)   { return "", false }
