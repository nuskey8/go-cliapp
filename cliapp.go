package cliapp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

type handler struct {
	fn           reflect.Value
	targs        []reflect.Type
	expectsError bool
	help         string
}

// Represents a small command-line application runtime.
type App struct {
	cmds map[string]handler
	root *handler
	opts *Options
}

// Configures runtime behavior for an App instance.
type Options struct {
	// when true the process will exit with code 1 on command
	ExitOnError bool

	// writer used for output. (default is os.Stdout)
	Log io.Writer

	// writer used for error messages. (default is os.Stderr)
	LogError io.Writer
}

// Create a new App with the default options.
func Default() *App {
	return New(Options{ExitOnError: true, Log: os.Stdout, LogError: os.Stderr})
}

// Create a new App with the provided options.
func New(opts Options) *App {
	if opts.Log == nil {
		opts.Log = os.Stdout
	}
	if opts.LogError == nil {
		opts.LogError = os.Stderr
	}
	app := &App{cmds: make(map[string]handler), opts: &opts}
	return app
}

// Add new command.
//
// This method supports two signatures:
//
//	Add(name string, fn func(...))
//	Add(name string, help string, fn func(...))
//
// Supported parameter types:
//
//	string, int, int64, float64, bool
func (a *App) Add(name string, rest ...any) {
	var help string
	var fn any
	switch len(rest) {
	case 1:
		fn = rest[0]
	case 2:
		if s, ok := rest[0].(string); ok {
			help = s
		} else {
			panic("help must be a string")
		}
		fn = rest[1]
	default:
		panic("Add method requires either (name, fn) or (name, help, fn)")
	}

	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		panic("handler must be a function")
	}

	ft := v.Type()
	nargs := ft.NumIn()
	targs := make([]reflect.Type, nargs)
	for i := range nargs {
		targs[i] = ft.In(i)
	}

	expectsErr := false
	nret := ft.NumOut()
	if nret > 0 && ft.Out(nret-1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		expectsErr = true
	}

	h := handler{fn: v, targs: targs, expectsError: expectsErr, help: help}
	if name == "" {
		// register root command
		a.root = &h
		return
	}

	a.cmds[name] = h
}

// Parses arguments and executes the matching command.
func (a *App) Run(args ...string) error {
	if args == nil {
		args = os.Args[1:]
	}

	if len(args) == 0 {
		// If root handler is registered, show its help as the default; otherwise show global help
		if a.root != nil {
			a.printCommandHelp("", *a.root)
			return nil
		}
		a.printHelp()
		return nil
	}

	// try help
	first := args[0]
	if first == "-h" || first == "--help" || first == "help" {
		// if a root handler exists, show root-specific usage; otherwise show general help
		if a.root != nil {
			a.printCommandHelp("", *a.root)
			return nil
		}
		a.printHelp()
		return nil
	}

	// match the longest registered command whose tokens are a prefix of args
	var bestName string
	var bestHandler handler
	var bestLen int
	for name, h := range a.cmds {
		// split registered name into tokens
		tokens := strings.Fields(name)
		if len(tokens) == 0 {
			continue
		}
		if len(tokens) > len(args) {
			continue
		}
		match := true
		for i, tok := range tokens {
			if args[i] != tok {
				match = false
				break
			}
		}
		if match && len(tokens) > bestLen {
			bestLen = len(tokens)
			bestName = name
			bestHandler = h
		}
	}

	if bestLen == 0 {
		// If a root handler (registered with name=="") exists, use it
		if a.root != nil {
			bestHandler = *a.root
			bestName = "(root)"
			// bestLen stays 0 so rawArgs := args[bestLen:] will be full args
		} else {
			return a.handleError(fmt.Errorf("unknown command: %s", first))
		}
	}

	h := bestHandler
	rawArgs := args[bestLen:]
	// per-command help: if next token is -h/--help show help for this command
	if len(rawArgs) > 0 {
		if rawArgs[0] == "-h" || rawArgs[0] == "--help" {
			a.printCommandHelp(bestName, h)
			return nil
		}
	}
	// Build parsed arguments. For primitive types we take positional args.
	parsed := make([]reflect.Value, len(h.targs))

	// If any target is a struct, we hand the whole remaining rawArgs to a struct parser
	// otherwise we parse positionally as before.
	usesStruct := false
	for _, t := range h.targs {
		if t.Kind() == reflect.Struct || (t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct) {
			usesStruct = true
			break
		}
	}

	if usesStruct {
		// For struct handlers we expect at most one struct parameter (common case).
		// We'll parse primitives positionally until we reach the struct param, then
		// parse the struct using flags/position tags from the remaining args.
		ri := 0 // index into rawArgs
		for i, t := range h.targs {
			// handle struct or pointer-to-struct
			if t.Kind() == reflect.Struct || (t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct) {
				var structType reflect.Type
				wantPtr := false
				if t.Kind() == reflect.Struct {
					structType = t
					wantPtr = false
				} else {
					structType = t.Elem()
					wantPtr = true
				}
				// parse struct from rawArgs[ri:]
				sv, nused, err := parseStructArgs(rawArgs[ri:], structType)
				if err != nil {
					return a.handleError(fmt.Errorf("failed to parse struct arg %d for %s: %w", i+1, bestName, err))
				}
				if wantPtr {
					parsed[i] = sv.Addr()
				} else {
					parsed[i] = sv
				}
				ri += nused
			} else {
				if ri >= len(rawArgs) {
					return a.handleError(fmt.Errorf("not enough arguments for %s: want %d, got %d", bestName, len(h.targs), len(rawArgs)))
				}
				v, err := parseValue(rawArgs[ri], t)
				if err != nil {
					return a.handleError(fmt.Errorf("failed to parse arg %d for %s: %w", i+1, bestName, err))
				}
				parsed[i] = v
				ri++
			}
		}
		// leftover args are ignored
	} else {
		// Check for unknown options
		for _, arg := range rawArgs {
			if strings.HasPrefix(arg, "--") {
				return a.handleError(fmt.Errorf("unknown option: %s", arg))
			}
		}
		if len(rawArgs) != len(h.targs) {
			return a.handleError(fmt.Errorf("wrong number of arguments for %s: want %d, got %d", bestName, len(h.targs), len(rawArgs)))
		}

		for i, t := range h.targs {
			v, err := parseValue(rawArgs[i], t)
			if err != nil {
				return a.handleError(fmt.Errorf("failed to parse arg %d for %s: %w", i+1, bestName, err))
			}
			parsed[i] = v
		}
	}

	res := h.fn.Call(parsed)

	if h.expectsError {
		// last return is error
		last := res[len(res)-1]
		if !last.IsNil() {
			return last.Interface().(error)
		}
	}

	return nil
}

func (a *App) handleError(err error) error {
	if err == nil {
		return nil
	}
	if a != nil && a.opts != nil && a.opts.ExitOnError {
		if a.opts.LogError != nil {
			fmt.Fprintln(a.opts.LogError, err.Error())
		} else {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		os.Exit(1)
	}
	return err
}

// Prints the common help and version options
func (a *App) printCommonOptions() {
	fmt.Fprintln(a.opts.Log, "Options:")
	fmt.Fprintln(a.opts.Log, "  -h|--help               Show this help")
}

// getTypeLabel returns a human-readable label for a type
func getTypeLabel(t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.String() {
	case "string":
		return "<string>"
	case "int":
		return "<int>"
	case "int64":
		return "<int64>"
	case "float64":
		return "<float64>"
	case "bool":
		return "<bool>"
	default:
		return "<value>"
	}
}

func (a *App) printHelp() {
	// If there is no root command, show a minimal Usage line that only
	// indicates options are available. If a root command exists, keep the
	// previous more verbose usage header.
	if a.root == nil {
		fmt.Fprintln(a.opts.Log, "Usage: [options...]")
		fmt.Fprintln(a.opts.Log)
	} else {
		fmt.Fprintln(a.opts.Log, "Usage:")
		fmt.Fprintln(a.opts.Log, "  command <args...> [options...]")
		fmt.Fprintln(a.opts.Log)
	}

	fmt.Fprintln(a.opts.Log, "Commands:")

	// compute max command name width for alignment
	max := 0
	for name := range a.cmds {
		if len(name) > max {
			max = len(name)
		}
	}
	for name, h := range a.cmds {
		if h.help != "" {
			fmt.Fprintf(a.opts.Log, "  %-*s  %s\n", max, name, h.help)
		} else {
			fmt.Fprintf(a.opts.Log, "  %s\n", name)
		}
	}
	fmt.Fprintln(a.opts.Log)

	a.printCommonOptions()
}

func (a *App) printCommandHelp(name string, h handler) {
	// If handler has help text, print it under Usage
	if h.help != "" {
		fmt.Fprintln(a.opts.Log, h.help)
		fmt.Fprintln(a.opts.Log)
	}

	// If the handler has only primitive (non-struct) parameters, treat each
	// parameter as a positional argument.
	// Note: Go reflection does not expose function parameter names, so we
	// use arg0, arg1, ... as the variable names.
	primitiveOnly := true
	for _, t := range h.targs {
		if t.Kind() == reflect.Struct || (t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct) {
			primitiveOnly = false
			break
		}
	}

	if primitiveOnly && len(h.targs) > 0 {
		// Usage: include command name (or program name when printing root)
		cmdName := name
		if cmdName == "" {
			// fall back to program name
			if len(os.Args) > 0 {
				cmdName = os.Args[0]
			} else {
				cmdName = "command"
			}
		}
		// Usage: cmd <args...>
		fmt.Fprintf(a.opts.Log, "Usage: %s <args...>\n", cmdName)
		fmt.Fprintln(a.opts.Log)

		// Arguments: show arg index, name (argN) and type
		fmt.Fprintln(a.opts.Log, "Arguments:")
		for i, t := range h.targs {
			tname := getTypeLabel(t)
			fmt.Fprintf(a.opts.Log, "  [%d] arg%d %s\n", i, i, tname)
		}
		fmt.Fprintln(a.opts.Log)

		// Options: only built-in help/version shown for primitive-only handlers
		a.printCommonOptions()
		return
	}

	// Usage header will be printed after we discover positional arguments

	// Collect positional args (arg tags)
	posMap := map[int]string{}
	maxPos := -1
	for _, t := range h.targs {
		var st reflect.Type
		if t.Kind() == reflect.Struct {
			st = t
		} else if t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct {
			st = t.Elem()
		} else {
			continue
		}
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			if v, ok := f.Tag.Lookup("arg"); ok {
				n, err := strconv.Atoi(v)
				if err == nil {
					// If description tag present, prefer it as the argument name
					if d, ok := f.Tag.Lookup("help"); ok && d != "" {
						posMap[n] = d
					} else {
						posMap[n] = toWords(f.Name)
					}
					if n > maxPos {
						maxPos = n
					}
				}
			}
		}
	}

	// Usage
	cmdName := name
	if cmdName == "" {
		if len(os.Args) > 0 {
			cmdName = os.Args[0]
		} else {
			cmdName = "command"
		}
	}
	if maxPos >= 0 {
		fmt.Fprintf(a.opts.Log, "Usage: %s <args...> [options...]\n", cmdName)
	} else {
		fmt.Fprintf(a.opts.Log, "Usage: %s [options...]\n", cmdName)
	}
	fmt.Fprintln(a.opts.Log)
	fmt.Fprintln(a.opts.Log)

	// Arguments section
	if maxPos >= 0 {
		fmt.Fprintln(a.opts.Log, "Arguments:")
		for i := 0; i <= maxPos; i++ {
			name := posMap[i]
			if name == "" {
				name = "arg" + strconv.Itoa(i)
			}
			fmt.Fprintf(a.opts.Log, "  [%d] %s\n", i, name)
		}
		fmt.Fprintln(a.opts.Log)
	}

	// If printing root usage (name == ""), include a Commands list of subcommands
	if name == "" {
		fmt.Fprintln(a.opts.Log, "Commands:")
		for cname, ch := range a.cmds {
			fmt.Fprintf(a.opts.Log, "  %s (args: %d)\n", cname, len(ch.targs))
		}
		fmt.Fprintln(a.opts.Log)
	}

	// Options
	fmt.Fprintln(a.opts.Log, "Options:")
	fmt.Fprintln(a.opts.Log, "  -h|--help               Show this help")

	// Print option fields (non-positional)
	for _, t := range h.targs {
		var st reflect.Type
		if t.Kind() == reflect.Struct {
			st = t
		} else if t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct {
			st = t.Elem()
		} else {
			continue
		}
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			tag := f.Tag
			if _, ok := tag.Lookup("arg"); ok {
				// skip positional fields from options
				continue
			}
			longName := "--" + toKebab(f.Name)
			if v, ok := tag.Lookup("long"); ok && v != "" {
				longName = v
			}
			shortName := ""
			if v, ok := tag.Lookup("short"); ok && v != "" {
				shortName = v
			}
			desc := ""
			if d, ok := tag.Lookup("help"); ok {
				desc = d
			}
			// Determine if this option should be shown as a flag (no value)
			// Treat bool and *bool as flags; ignore explicit `flag` tag.
			isFlag := false
			if f.Type.Kind() == reflect.Bool || (f.Type.Kind() == reflect.Ptr && f.Type.Elem().Kind() == reflect.Bool) {
				isFlag = true
			}

			typeLabel := ""
			if !isFlag {
				typeLabel = " " + getTypeLabel(f.Type)
			}

			if shortName != "" {
				fmt.Fprintf(a.opts.Log, "  %s|%s%s    %s\n", shortName, longName, typeLabel, desc)
			} else {
				fmt.Fprintf(a.opts.Log, "  %s%s    %s\n", longName, typeLabel, desc)
			}
		}
	}
}

// parseValue parses a string value to the given target type
func parseValue(s string, targetType reflect.Type) (reflect.Value, error) {
	switch targetType.Kind() {
	case reflect.String:
		return reflect.ValueOf(s), nil
	case reflect.Int:
		v, err := strconv.Atoi(s)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(v), nil
	case reflect.Int64:
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(v), nil
	case reflect.Float64:
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(v), nil
	case reflect.Bool:
		v, err := strconv.ParseBool(s)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(v), nil
	default:
		return reflect.Value{}, errors.New("unsupported parameter type: " + targetType.String())
	}
}

// Parses a string value and sets it to a field, handling pointer types
func parseAndSetField(field reflect.Value, value string) error {
	fieldType := field.Type()

	// Handle pointer types
	if fieldType.Kind() == reflect.Ptr {
		elemType := fieldType.Elem()
		parsedValue, err := parseValue(value, elemType)
		if err != nil {
			return err
		}
		ptr := reflect.New(elemType)
		ptr.Elem().Set(parsedValue)
		field.Set(ptr)
		return nil
	}

	// Handle direct types
	parsedValue, err := parseValue(value, fieldType)
	if err != nil {
		return err
	}
	field.Set(parsedValue)
	return nil
}

// Sets a boolean field (including pointer types) to true
func setBoolField(field reflect.Value) {
	fieldType := field.Type()

	if fieldType.Kind() == reflect.Bool {
		field.SetBool(true)
	} else if fieldType.Kind() == reflect.Ptr && fieldType.Elem().Kind() == reflect.Bool {
		ptr := reflect.New(fieldType.Elem())
		ptr.Elem().SetBool(true)
		field.Set(ptr)
	}
}

// Checks if a field is a boolean or pointer to boolean
func isBoolField(fieldType reflect.Type) bool {
	return fieldType.Kind() == reflect.Bool ||
		(fieldType.Kind() == reflect.Ptr && fieldType.Elem().Kind() == reflect.Bool)
}

// Builds lookup maps for struct fields based on their tags
func buildFieldMaps(t reflect.Type) (map[int]int, map[string]int, map[string]int) {
	posFields := make(map[int]int) // position -> field index in struct
	longMap := make(map[string]int)
	shortMap := make(map[string]int)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag

		if v, ok := tag.Lookup("arg"); ok {
			// parse integer for positional args
			n, err := strconv.Atoi(v)
			if err == nil {
				posFields[n] = i
			}
		} else {
			// no arg tag => default to named option with kebab-case long name
			longName := "--" + toKebab(f.Name)
			if v, ok := tag.Lookup("long"); ok {
				longName = v
			}
			longMap[longName] = i

			if v, ok := tag.Lookup("short"); ok {
				shortMap[v] = i
			}
		}

		// if arg tag existed, still allow explicit long/short mapping
		if v, ok := tag.Lookup("long"); ok {
			longMap[v] = i
		}
		if v, ok := tag.Lookup("short"); ok {
			shortMap[v] = i
		}
	}

	return posFields, longMap, shortMap
}

// Parses command line args into a struct value of type t.
// It returns the reflect.Value (addressable) and the number of raw args consumed.
//
// Supported tags on struct fields:
//
//   - `arg:"N"`  - positional argument index (0-based) relative to the remaining args
//   - `long:"--name"` - long option name
//   - `short:"-n"` - short option name
//   - `flag` - boolean flag (no value required)
func parseStructArgs(raw []string, t reflect.Type) (reflect.Value, int, error) {
	if t.Kind() != reflect.Struct {
		return reflect.Value{}, 0, errors.New("parseStructArgs: t must be struct")
	}

	// create a new struct value
	sv := reflect.New(t).Elem()

	// Build lookup tables for long/short options and positional fields
	posFields, longMap, shortMap := buildFieldMaps(t)

	consumed := 0

	// First handle positional args: collect by increasing position index
	if len(posFields) > 0 {
		// find max index
		max := -1
		for k := range posFields {
			if k > max {
				max = k
			}
		}
		// for positions 0..max, consume from raw accordingly
		for p := 0; p <= max; p++ {
			fi, ok := posFields[p]
			if !ok {
				// skip
				continue
			}
			if consumed >= len(raw) {
				return reflect.Value{}, consumed, fmt.Errorf("not enough positional args for struct: need position %d", p)
			}
			f := sv.Field(fi)
			err := parseAndSetField(f, raw[consumed])
			if err != nil {
				return reflect.Value{}, consumed, fmt.Errorf("failed to parse positional arg at position %d: %w", p, err)
			}
			consumed++
		}
	}

	// Next, scan remaining raw args for long/short options and flags
	i := consumed
	for i < len(raw) {
		tok := raw[i]
		// long form --name or --name=val
		if strings.HasPrefix(tok, "--") {
			// split on =
			if eq := strings.Index(tok, "="); eq != -1 {
				name := tok[:eq]
				val := tok[eq+1:]
				if fi, ok := longMap[name]; ok {
					f := sv.Field(fi)
					err := parseAndSetField(f, val)
					if err != nil {
						return reflect.Value{}, consumed, fmt.Errorf("failed to parse value for option %s: %w", name, err)
					}
				}
				i++
				continue
			}
			// separate value in next token
			name := tok
			if fi, ok := longMap[name]; ok {
				f := sv.Field(fi)
				ft := f.Type()
				// flag handling: both bool and *bool should be treated as flags
				if isBoolField(ft) {
					setBoolField(f)
					i++
					continue
				}
				if i+1 >= len(raw) {
					return reflect.Value{}, consumed, fmt.Errorf("missing value for %s", name)
				}
				err := parseAndSetField(f, raw[i+1])
				if err != nil {
					return reflect.Value{}, consumed, fmt.Errorf("failed to parse value for option %s: %w", name, err)
				}
				i += 2
				continue
			}
			// unknown long option: error
			return reflect.Value{}, consumed, fmt.Errorf("unknown option: %s", tok)
		}

		// short form -x (maybe combined like -ab not supported) or -o val
		if strings.HasPrefix(tok, "-") && len(tok) >= 2 {
			// treat as short option key exactly as given
			if fi, ok := shortMap[tok]; ok {
				f := sv.Field(fi)
				ft := f.Type()
				// flag handling for short options as well (bool and *bool)
				if isBoolField(ft) {
					setBoolField(f)
					i++
					continue
				}
				if i+1 >= len(raw) {
					return reflect.Value{}, consumed, fmt.Errorf("missing value for %s", tok)
				}
				err := parseAndSetField(f, raw[i+1])
				if err != nil {
					return reflect.Value{}, consumed, fmt.Errorf("failed to parse value for option %s: %w", tok, err)
				}
				i += 2
				continue
			}
			// unknown short option: error
			return reflect.Value{}, consumed, fmt.Errorf("unknown option: %s", tok)
		}

		// positional leftover without explicit tag: stop scanning options
		break
	}

	return sv, consumed, nil
}

// Converts CamelCase/PascalCase to space-separated lowercase words.
//   - FilePath -> file path
func toWords(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Converts CamelCase/PascalCase to kebab-case
//   - OutDir -> out-dir
func toKebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
