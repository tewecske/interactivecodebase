// Package cobra is a stub of github.com/spf13/cobra with the parts the
// fixture uses.
package cobra

import "context"

type Command struct {
	Use   string
	Short string
	Run   func(cmd *Command, args []string)
	RunE  func(cmd *Command, args []string) error

	children []*Command
}

func (c *Command) Context() context.Context { return context.Background() }

func (c *Command) AddCommand(cmds ...*Command) { c.children = append(c.children, cmds...) }

func (c *Command) Execute() error {
	for _, child := range c.children {
		if child.RunE != nil {
			if err := child.RunE(child, nil); err != nil {
				return err
			}
		}
		if child.Run != nil {
			child.Run(child, nil)
		}
	}
	return nil
}
