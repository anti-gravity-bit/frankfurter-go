// Command fx is a tiny CLI over the frankfurter client.
//
//	fx rate EUR INR
//	fx convert 10 EUR INR
//	fx rates -base EUR -quotes USD,INR [-date 2024-05-15 | -from 2026-10-01 -to 2026-10-03]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/anti-gravity-bit/frankfurter-go/frankfurter"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, frankfurter.New()); err != nil {
		var apiErr *frankfurter.APIError
		if errors.As(err, &apiErr) {
			fmt.Fprintf(os.Stderr, "fx: API error %d: %s\n", apiErr.StatusCode, apiErr.Message)
		} else {
			fmt.Fprintln(os.Stderr, "fx:", err)
		}
		os.Exit(1)
	}
}

const usage = "usage: fx rate BASE QUOTE | fx convert AMOUNT BASE QUOTE | fx rates [-base B] [-quotes Q1,Q2] [-date D | -from D -to D]"

func run(ctx context.Context, args []string, out io.Writer, c *frankfurter.Client) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	switch args[0] {
	case "rate":
		if len(args) != 3 {
			return errors.New(usage)
		}
		r, err := c.Rate(ctx, args[1], args[2], time.Time{})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s 1 %s = %g %s\n", r.Date, r.Base, r.Rate, r.Quote)
	case "convert":
		if len(args) != 4 {
			return errors.New(usage)
		}
		amt, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			return fmt.Errorf("bad amount %q: %w", args[1], err)
		}
		v, r, err := c.Convert(ctx, amt, args[2], args[3])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%g %s = %.2f %s (rate %g, %s)\n", amt, r.Base, v, r.Quote, r.Rate, r.Date)
	case "rates":
		fs := flag.NewFlagSet("rates", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		base := fs.String("base", "", "base currency")
		quotes := fs.String("quotes", "", "comma-separated quote currencies")
		date := fs.String("date", "", "YYYY-MM-DD")
		from := fs.String("from", "", "YYYY-MM-DD")
		to := fs.String("to", "", "YYYY-MM-DD")
		if err := fs.Parse(args[1:]); err != nil {
			return fmt.Errorf("%w\n%s", err, usage)
		}
		if fs.NArg() > 0 {
			// Without this, "fx rates EUR USD" silently ignored its arguments
			// and printed every currency (found in the reproducibility run).
			return fmt.Errorf("rates takes flags, not positional arguments (got %q); try: fx rates -base EUR -quotes USD,INR\n%s",
				strings.Join(fs.Args(), " "), usage)
		}
		q := frankfurter.RatesQuery{Base: *base}
		if *quotes != "" {
			q.Quotes = strings.Split(*quotes, ",")
		}
		var err error
		for _, p := range []struct {
			s   string
			dst *time.Time
		}{{*date, &q.Date}, {*from, &q.From}, {*to, &q.To}} {
			if p.s == "" {
				continue
			}
			if *p.dst, err = time.Parse("2006-01-02", p.s); err != nil {
				return fmt.Errorf("bad date %q: %w", p.s, err)
			}
		}
		rs, err := c.Rates(ctx, q)
		if err != nil {
			return err
		}
		for _, r := range rs {
			fmt.Fprintf(out, "%s %s/%s %g\n", r.Date, r.Base, r.Quote, r.Rate)
		}
	default:
		return errors.New(usage)
	}
	return nil
}
