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
// Two things are derived here, and they answer different questions:
//
//	overflow      a declared bound larger than the column it resolves to.
//	              This needs the mapping from a wire field to a column, and
//	              reaches only as far as that mapping does.
//	disagreement  two surfaces stating different bounds for one field. This
//	              needs no column: whichever of the two is the column's
//	              width, the other is not, so one of them is wrong. It holds
//	              everywhere the field is declared twice.
//
// The mapping is derived rather than listed, from the two kinds of evidence
// the repository already carries — the resource an input type or a tool
// names, and the tables that surface's own statements write. Resolve
// states the rule. It does not reach every field, and it is not meant to:
// most bounded inputs are not stored under the name they arrive as, and
// requiring each of those to carry an exemption would put a marker on the
// majority of them, which teaches people to write markers rather than to
// look. So an unresolved field is not a failure. It is counted and printed,
// so the size of the gap is visible rather than assumed.
//
//	scope        every wire field of a body reachable from a type named
//	             *Input under the handler trees, and every property of an
//	             MCP tool's input schema, that states a maxLength.
//	resolution   the owner names a write verb and a resource; a table this
//	             surface writes is that resource pluralised; the wire name
//	             in the schema's spelling is a column of it. One candidate
//	             or none — two is no answer.
//	comparison   the character width of VARCHAR and CHAR, and the byte
//	             capacity of the text types, which bounds a character count
//	             from above. An ENUM states a value set rather than a
//	             length and is left to the check that reads value sets.
package columnbounds
