// example/check — q.Check and q.CheckE for functions returning just
// error (file.Close, db.Ping, validate). Both are always expression
// statements — they return nothing. Run with:
//
//	go run -toolexec=q ./example/check
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ErrAlreadyClosed is the "benign" error we'll swallow below.
var ErrAlreadyClosed = errors.New("already closed")

// closer returns one of three possibilities: nil (success),
// ErrAlreadyClosed (benign), or a real error.
func closer(mode string) error {
	switch mode {
	case "ok":
		return nil
	case "closed":
		return ErrAlreadyClosed
	}
	return errors.New("close failed: " + mode)
}

// bareCheck — bubble the captured err unchanged.
func bareCheck(mode string) error {
	q.Check(closer(mode))
	return nil
}

// checkWithErr — substitute a constant error on the bubble.
func checkWithErr(mode string) error {
	q.CheckE(closer(mode)).Err(errors.New("replaced"))
	return nil
}

// checkWithWrap — attach a prefix via %w-wrapping.
func checkWithWrap(mode string) error {
	q.CheckE(closer(mode)).Wrap("shutting down")
	return nil
}

// checkWithWrapf — like Wrap but with format args. Format must be
// a string literal.
func checkWithWrapf(mode string, id int) error {
	q.CheckE(closer(mode)).Wrapf("shutting down id=%d", id)
	return nil
}

// checkCatchSuppress — CheckE.Catch returning nil swallows the
// error (execution falls through past the Check). Returning
// non-nil bubbles that error in place of the original.
func checkCatchSuppress(mode string) error {
	q.CheckE(closer(mode)).Catch(func(e error) error {
		if errors.Is(e, ErrAlreadyClosed) {
			return nil // swallow benign errors
		}
		return e
	})
	return nil
}

// Realistic use: a shutdown sequence that wraps each step's error
// but swallows "already closed" on cleanup.
func shutdown(mode string) error {
	q.CheckE(closer(mode)).Wrap("step 1")
	q.CheckE(closer(mode)).Catch(func(e error) error {
		if errors.Is(e, ErrAlreadyClosed) {
			return nil
		}
		return e
	})
	return nil
}

func main() {
	cases := []struct {
		name string
		fn   func(string) error
	}{
		{"bareCheck", bareCheck},
		{"checkWithErr", checkWithErr},
		{"checkWithWrap", checkWithWrap},
		{"checkCatchSuppress", checkCatchSuppress},
		{"shutdown", shutdown},
	}

	for _, c := range cases {
		for _, mode := range []string{"ok", "closed", "bad"} {
			err := c.fn(mode)
			if err != nil {
				fmt.Printf("%-22s(%q) => err: %v\n", c.name, mode, err)
			} else {
				fmt.Printf("%-22s(%q) => ok\n", c.name, mode)
			}
		}
	}

	// Wrapf takes a separate signature so we exercise it alone.
	for _, mode := range []string{"ok", "bad"} {
		err := checkWithWrapf(mode, 42)
		if err != nil {
			fmt.Printf("%-22s(%q) => err: %v\n", "checkWithWrapf", mode, err)
		} else {
			fmt.Printf("%-22s(%q) => ok\n", "checkWithWrapf", mode)
		}
	}
}
