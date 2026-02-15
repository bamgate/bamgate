//go:build tools

package mobile

// This import ensures golang.org/x/mobile/bind stays in go.mod.
// gobind (the code generator invoked by gomobile) needs this package
// at code-generation time but it's never imported by our source code directly.
import _ "golang.org/x/mobile/bind"
