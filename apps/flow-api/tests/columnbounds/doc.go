// Package columnbounds derives, from the committed sources, the request
// fields whose declared length the storage behind them cannot hold.
//
// A length bound on an input is a promise: the API tells the caller, and
// tells every generated client through the OpenAPI document and the tool
// schema, that a string this long is acceptable. When the column the value
// lands in is narrower, the promise is kept only up to the column's width.
// Past it the request passes validation and is refused by MySQL, so the
// caller receives a server error for input the API said it would take —
// and the error names nothing they can act on, because as far as the
// contract goes their input was fine.
//
// Three things are derived here, and they answer different questions:
//
//	overflow      a declared bound larger than the column it resolves to.
//	              This needs the mapping from a wire field to a column, and
//	              reaches only as far as that mapping does.
//	disagreement  two surfaces stating different bounds for one field. This
//	              needs no column: whichever of the two is the column's
//	              width, the other is not, so one of them is wrong. It holds
//	              everywhere the field is declared twice.
//	absent        a string field that resolves to a column of bounded width
//	              and states no bound at all. It needs the same mapping the
//	              overflow half needs, and names the same gap with the wire
//	              side left open instead of set too wide: everything past
//	              the column's width validates and is refused by storage,
//	              and the caller is told nothing about where the line is,
//	              because the contract draws none. Neither of the other two
//	              sees it — an undeclared bound is wider than nothing, and
//	              two surfaces declaring nothing agree perfectly.
//
// All three refuse. A field the mapping reaches has a width behind it, and
// a field that has one and does not say so is as wrong as one that says the
// wrong number; the only reason to treat it differently would be that there
// were many of them, which is a fact about a moment rather than about any
// field.
//
// The mapping is derived rather than listed, and there are two derivations
// because there are two independent kinds of evidence. One reads what an
// operation is called: a name carrying a write verb states the resource, and
// the tables its own statements write say which of them that is. The other
// reads what an operation does: the statements the function taking the input
// calls, and the tables those statements write. Neither is a vocabulary
// anybody maintains, and the second exists because the first cannot see an
// operation whose name states no verb — presigning an upload, proposing a
// plan, applying a recurrence all store something, and the next name nobody
// thought of would be outside any list of verbs written today. Resolve and
// ResolveByCalls state the two rules, in that order: the name is the
// stronger evidence and goes first.
//
// Between them they do not reach every field, and they are not meant to:
// most inputs are not stored under the name they arrive as, and requiring
// each of those to carry an exemption would put a marker on the majority of
// them, which teaches people to write markers rather than to look. So an
// unresolved field is not a failure, whether it states a bound or states
// none. It is counted and printed, so the size of the gap is visible rather
// than assumed.
//
//	scope        every string wire field of a body reachable from a type
//	             named *Input under the handler trees, and every string
//	             property of an MCP tool's input schema, whether or not it
//	             states a maxLength. A field of some other type is outside
//	             this: an integer, a boolean and a date are not constrained
//	             by a length, and a field naming the values it accepts is
//	             bounded by that set, which already fixes its longest value.
//	resolution   the owner names a write verb and a resource, a table this
//	             surface writes is that resource pluralised, and the wire
//	             name in the schema's spelling is a column of it; or, for
//	             what that leaves over, the handler taking the input calls a
//	             statement, and one table that statement writes carries the
//	             column. Either way one candidate or none — two is no answer.
//	             A field nested under an object is reached by the second rule
//	             only, under the name of its last segment: the resource an
//	             input is named after says nothing about a member of some
//	             other object, while the statements a handler calls answer
//	             for that member as readily as for the body's own fields.
//	comparison   the character width of VARCHAR and CHAR, and the byte
//	             capacity of the text types, which bounds a character count
//	             from above. An ENUM states a value set rather than a
//	             length and is left to the check that reads value sets.
//
// That other check lives beside this one and shares this resolution rather
// than repeating it: the ENUM columns are read here, and reaching them is a
// matter of which columns a lookup may answer with. Schema.EnumsOnly and
// RESTDeclaration are exported for it, and for nothing else.
package columnbounds
