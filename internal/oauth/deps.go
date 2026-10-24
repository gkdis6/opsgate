package oauth

// Pins dependencies for parallel work; delete once real code imports them.
import (
	_ "github.com/coreos/go-oidc/v3/oidc"
	_ "golang.org/x/oauth2"
	_ "golang.org/x/oauth2/github"
	_ "golang.org/x/time/rate"
)
