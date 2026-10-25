package oauth

import "net/http"

// A response captures only request-local values. Handlers prepare it while
// holding mu, then send it after releasing mu so slow clients cannot stall auth.
type response func(http.ResponseWriter, *http.Request)

func jsonResponse(status int, data any) response {
	return func(w http.ResponseWriter, _ *http.Request) { jsonReply(w, status, data) }
}
func errorResponse(status int, code string) response {
	return jsonResponse(status, map[string]string{"error": code})
}
func htmlResponse(status int, data pageData) response {
	return func(w http.ResponseWriter, _ *http.Request) { htmlPage(w, status, data) }
}
func htmlErrorResponse(status int, message string) response {
	return htmlResponse(status, pageData{Error: message})
}
func redirectResponse(location string) response {
	return func(w http.ResponseWriter, r *http.Request) {
		htmlHeaders(w)
		http.Redirect(w, r, location, http.StatusSeeOther)
	}
}
func (reply response) withCookie(name, value string, age int) response {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie(w, name, value, age)
		reply(w, r)
	}
}
