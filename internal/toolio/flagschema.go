package toolio

import (
	"flag"
	"strconv"
	"sync"
	"time"
)

// enums records the closed set of legal values of the flags that have one.
// flag.Flag carries nothing about whether its legal values are closed, so the
// pairing lives here, keyed by the registered flag itself.
var enums sync.Map // *flag.Flag -> []string

// DeclareEnum pairs the flag named name, already registered on fs, with the
// closed set of values it accepts, so the flags document can list them. A
// name that is not registered is ignored: there is nothing to describe.
func DeclareEnum(fs *flag.FlagSet, name string, values []string) {
	f := fs.Lookup(name)
	if f == nil {
		return
	}
	enums.Store(f, append([]string(nil), values...))
}

// flagsExcluded are the flags a caller never passes as part of a tool call:
// both make the process return before doing anything else.
var flagsExcluded = map[string]bool{"schema": true, "version": true}

// BuildFlagsDocument describes every flag registered on fs as a standalone
// JSON Schema 2020-12 document. It walks fs.VisitAll, so properties come in
// flag-name order, and leaves out --schema and --version.
//
// Nothing is marked required (every flag has a default) and no
// additionalProperties restriction is asserted; how strictly to use the
// document is the caller's decision.
func BuildFlagsDocument(fs *flag.FlagSet) SchemaObject {
	props := SchemaObject{}
	fs.VisitAll(func(f *flag.Flag) {
		if flagsExcluded[f.Name] {
			return
		}
		props = append(props, SchemaField{Key: f.Name, Value: flagProperty(f)})
	})
	return SchemaObject{
		{Key: "$schema", Value: SchemaDialect},
		{Key: "type", Value: "object"},
		{Key: "properties", Value: props},
	}
}

// flagProperty is the schema of one flag.
func flagProperty(f *flag.Flag) SchemaObject {
	var p SchemaObject
	typ, format, def := flagType(f)
	p.Set("type", typ)
	if format != "" {
		p.Set("format", format)
	}
	if e, ok := enums.Load(f); ok {
		p.Set("enum", e.([]string))
	}
	p.Set("default", def)
	p.Set("description", f.Usage)
	return p
}

// flagType infers a flag's JSON type, format and typed default from its
// concrete Value. A Value that is not a flag.Getter is reported as a string
// whose default is its own String form, as captured when it was registered.
func flagType(f *flag.Flag) (typ, format string, def any) {
	g, ok := f.Value.(flag.Getter)
	if !ok {
		return "string", "", f.DefValue
	}
	switch g.Get().(type) {
	case bool:
		if b, err := strconv.ParseBool(f.DefValue); err == nil {
			return "boolean", "", b
		}
	case int, int8, int16, int32, int64:
		if n, err := strconv.ParseInt(f.DefValue, 10, 64); err == nil {
			return "integer", "", n
		}
	case uint, uint8, uint16, uint32, uint64:
		if n, err := strconv.ParseUint(f.DefValue, 10, 64); err == nil {
			return "integer", "", n
		}
	case float32, float64:
		if n, err := strconv.ParseFloat(f.DefValue, 64); err == nil {
			return "number", "", n
		}
	case time.Duration:
		return "string", "duration", f.DefValue
	case string:
		return "string", "", f.DefValue
	}
	return "string", "", f.DefValue
}
