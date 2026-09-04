// Package responseids derives, from the committed sources, the MCP tool
// responses that carry a row's internal identifier.
//
// A row is addressed two ways. Inside the database it is an unsigned
// counter, dense and guessable, and it is what every foreign key, join and
// lock is written against. On the wire it is a UUID v7, which is what a
// caller may hold, quote back and be refused on. Handing the counter out
// collapses that separation for good: it tells the holder how many rows of
// that kind exist, it lets them address a row they were never shown, and it
// cannot be withdrawn afterwards, because a client that learned an id keeps
// it.
//
// Nothing about a tool's response is declared. Each handler builds a
// `map[string]any` by hand and returns it, so there is no schema between the
// row and the transport that a wrong field would fail to satisfy: a tool
// written as `return map[string]any{"id": row.ID, ...}, nil` compiles,
// serialises and ships. The separation therefore holds by reading, and this
// derives what a reader would have to notice.
//
// Two shapes are refused, and they fail differently:
//
//	value    an internal id reached through a selector and placed under a
//	         key. The key is usually right — "id", "taskId" — and the
//	         expression under it is the internal spelling of the same row.
//	whole    a row returned as itself. Nothing is spelled wrong here
//	         because nothing is spelled at all: the marshaller walks the
//	         model and emits every field it is not told to skip.
//
// The vocabulary of internal ids is derived rather than listed, which is
// what makes this survive a table added tomorrow. Two pieces of evidence
// have to agree, and each answers a question the other cannot:
//
//	type     an unsigned integer is a surrogate key's Go spelling, and a
//	         nullable signed integer is the same key where the column
//	         admits NULL. A public id is a UUID type and a timestamp is a
//	         time, so neither is reachable this way.
//	tag      the generated model marks the column as never serialised.
//	         Type alone would collect the ordinary counters beside them —
//	         an attempt count, a context window, a sampling temperature —
//	         which are values a response is entitled to carry, and flagging
//	         those would be answered with exemptions rather than with a
//	         reading.
//
//	scope    every `func run*(ctx, deps, s, raw) (any, error)` under
//	         internal/mcp, which is the shape every tool handler is
//	         written in; every `map[string]any` literal inside one,
//	         wherever it is nested and whether it is returned directly or
//	         appended into a slice the response carries; every literal of a
//	         type the package declares, since a list-shaped tool states its
//	         row that way and the map above it names one key; and every
//	         return of an identifier a statement call assigned. A field the
//	         marshaller cannot see — unexported, or tagged away — carries
//	         nothing to a caller and is not read.
//	evidence a selector's field, read the nearer of two ways. Where the
//	         base resolves to a struct the sources state — the parsed
//	         arguments, a response shape declared beside the handler, the
//	         session the transport hands in — the field's own declared type
//	         answers, which is both what tells the arguments' taskId, a
//	         public string, apart from the column of the same name, and
//	         what reaches the session's workspace counter that no model
//	         spells. Otherwise the derived vocabulary answers on the field
//	         name.
//
// It reads syntax and nothing else, so what it cannot see is worth stating.
// An id copied into a local of some other name before it reaches the map is
// invisible; so is one that leaves through a helper the handler calls, since
// the value the map carries is then the helper's result rather than a field.
// A response assembled by a function that is not a tool handler is outside
// the walk. None of that is exempted — it is unreached, which is a different
// thing, and the counts a run prints are what says how much was reached.
package responseids
