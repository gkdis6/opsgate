package tool

import "strings"

// Undo is a catalog action that reverses an applied write. It is only ever
// offered as a fresh approval link; nothing applies it automatically.
type Undo struct {
	Tool string
	Args map[string]any
}

// UndoPrefix starts the last line of a write result that offers an undo link.
const UndoPrefix = "undo_url: "

// undoLine returns the result suffix for an applied plan, or "" when the plan
// has no undo or the server cannot build a link for it.
func (env *Env) undoLine(p Plan) string {
	if p.Undo == nil || env.UndoURL == nil {
		return ""
	}
	u := env.UndoURL(*p.Undo)
	if u == "" || strings.ContainsAny(u, "\r\n") {
		return ""
	}
	return "\n" + UndoPrefix + u
}

// SplitUndo separates a write result from its trailing undo link line.
func SplitUndo(text string) (result, undoURL string) {
	i := strings.LastIndex(text, "\n"+UndoPrefix)
	if i < 0 || strings.Contains(text[i+1:], "\n") {
		return text, ""
	}
	return text[:i], text[i+1+len(UndoPrefix):]
}
