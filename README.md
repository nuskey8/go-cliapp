# go-cliapp

![](./assets/hero.png)

go-cliapp is a framework for creating command-line applications in Go.

English | [日本語](./README_JA.md)

## Why go-cliapp?

go-cliapp differs from other CLI libraries for Go by adopting a routing-style API commonly seen in many web frameworks. This significantly simplifies the code, making development and maintenance easier.

For example, let's implement a CLI tool that adds two numbers using [spf13/cobra](https://github.com/spf13/cobra), [urfave/cli](https://github.com/urfave/cli), and go-cliapp.

<details>

<summary>spf13/cobra</summary>

```go
package main

import (
    "fmt"
    "os"
    "strconv"

    "github.com/spf13/cobra"
)

func main() {
    var rootCmd = &cobra.Command{}

    var addCmd = &cobra.Command{
        Use:   "add [num1] [num2]",
        Short: "Add two numbers",
        Args:  cobra.ExactArgs(2),
        Run: func(cmd *cobra.Command, args []string) {
            num1, err1 := strconv.Atoi(args[0])
            num2, err2 := strconv.Atoi(args[1])
            if err1 != nil || err2 != nil {
                fmt.Println("Please provide two valid integers.")
                return
            }
            fmt.Printf("Result: %d\n", num1+num2)
        },
    }

    rootCmd.AddCommand(addCmd)

    if err := rootCmd.Execute(); err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
}
```

</details>

<details>

<summary>urfave/cli</summary>

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/urfave/cli/v3"
)

func main() {
    cmd := cli.Command{
		Name:  "add",
		Usage: "Add two integers",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 2 {
				return fmt.Errorf("please provide exactly two integers")
			}

			num1, err1 := strconv.Atoi(cmd.Args().Get(0))
			num2, err2 := strconv.Atoi(cmd.Args().Get(1))
			if err1 != nil || err2 != nil {
				return fmt.Errorf("please provide valid integers")
			}

			fmt.Printf("Result: %d\n", num1+num2)
			return nil
		},
	}

    err := cmd.Run(context.Background(), os.Args)
    if err != nil {
        fmt.Println(err)
    }
}
```

</details>

<details>

<summary>go-cliapp</summary>

```go
package main

import (
	"fmt"

	"github.com/nuskey8/go-cliapp"
)

func main() {
	app := cliapp.Default()

	app.Add("add", "Add two integers", func(num1 int, num2 int) {
		fmt.Printf("Result: %d\n", num1+num2)
	})

	app.Run()
}
```

</details>

As you can see, the same functionality can be implemented with much simpler code.

Additionally, go-cliapp comes with many powerful features:

* Support for subcommands
* Automatic generation of human-friendly help
* Advanced mapping using struct tags
* Flag parsing
* Support for short options (e.g., `-o`)
* Customizable log output
* Command argument customization
* Error handling
* No dependencies outside the standard library

## Quick Start

Create an `App` using `cliapp.Default()`, add commands with `Add()`, and execute them with `Run()`. go-cliapp dynamically generates commands by analyzing the signature of the provided functions.

```go
package main

import (
	"fmt"

	"github.com/nuskey8/go-cliapp"
)

func main() {
	app := cliapp.Default()

	app.Add("echo", func(msg string) {
		fmt.Println(msg)
	})

	app.Run()
}
```

Then execute the following:

```
$ go run main echo hello
hello
```

By adding `-h` or `--help`, you can view the generated help message.

```
$ go run main echo -h
Usage:
  echo <args...>

Arguments:
  [0] arg0 <string>

Options:
  -h|--help               Show this help
```

Arguments are automatically validated based on the function signature, and errors are displayed if the number or type of arguments is incorrect.

```go
app.Add("add", func(num1 int, num2 int) {
    fmt.Printf("Result: %d\n", num1+num2)
})
```

```
$ go run main add 1
wrong number of arguments for add: want 2, got 1

$ go run main add 1 hello
failed to parse arg 2 for add: strconv.Atoi: parsing "hello": invalid syntax
```

## Command Descriptions

You can also add a description to a command by providing it as the second argument to `app.Add()`.

```go
app.Add("add", "Add two numbers", func(num1 int, num2 int) {
    fmt.Printf("Result: %d\n", num1+num2)
})
```

## Subcommands

You can create subcommands by separating command names with spaces.

```go
app.Add("foo", func() {
    fmt.Println("Foo")
})

app.Add("foo bar", func() {
    fmt.Println("Foo Bar")
})
```

## Error Handling

Functions passed to commands can return `error`. By default, if a command returns an `error`, the process exits with `os.Exit(1)`.

```go
app.Add("error", func() error {
    errors.New("something wrong!")
})
```

## Mapping to Structs

If a command has a complex signature or needs to support flags, you can receive arguments as a `struct`.

```go
// Omitted

type CreateTextArgs struct {
	Input  string 
	Output string 
}

func main() {
	app := cliapp.Default()

	app.Add("newtxt", "Create a text file from input", func(args *CreateTextArgs) error {
		if args == nil {
			return errors.New("missing args")
		}

		outPath := "out.txt"
		if args.Output != nil {
			outPath = *args.Output
		}

		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = f.WriteString(args.Input)
		if err != nil {
			return err
		}

		return nil
	})

	app.Run()
}
```

```
$ go run main newtxt -h
Create a text file from input

Usage:
  newtxt [options...]

Options:
  -h|--help               Show this help
  --input <string>    
  --output <string> 
   
$ go run main newtxt --input hello --output hello.txt
```

The supported struct types are the same as those accepted as function arguments. However, `bool` types are interpreted as flags.

By making a field a pointer type, you can make the option optional.

```go
type CreateTextArgs struct {
	Input  string 
	Output *string  // Optional
}
```

You can also customize options by adding tags to fields.

```go
type CreateTextArgs struct {
	Input  string  `arg:"0" help:"input file path"`
	Output *string `short:"-o" help:"output file path"`
}
```

This can be used as follows:

```
$ go run main newtxt -h
Create a text file from input

Usage:
  newtxt <args...> [options...]

Arguments:
  [0] input file path

Options:
  -h|--help               Show this help
  -o|--output <string>    output file path
```

The supported tags are as follows:

| Tag Name | Example                         | Description                                                                                            |
| -------- | ------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `long`   | `` `long:"--output"` ``         | Specifies the name of the long option. Defaults to [`--` + field name in kebab-case].                  |
| `short`  | `` `short:"-o"` ``              | Specifies the name of the short option.                                                                |
| `help`   | `` `help:"output file path"` `` | Specifies the description of the argument displayed in the help option.                                |
| `arg`    | `` `arg:"0"` ``                 | Changes the field to be treated as an argument instead of an option. The value specifies the position. |

## cliapp.Options

You can customize the behavior of the `App` itself using `cliapp.New()`.

```go
app := cliapp.New(cliapp.Options{
    ExitOnError: true,      // Calls os.Exit(1) on error if true
    Log:         io.Stdout, // Log output destination
    LogError:    io.Stderr, // Error output destination
})
```

## Custom Command Arguments

By default, go-cliapp parses `os.Args()[1:]`, but you can manually pass arguments to `Run()`.

```go
app.Run("foo", "bar")
```

## License

This library is released under the [MIT License](./LICENSE).
