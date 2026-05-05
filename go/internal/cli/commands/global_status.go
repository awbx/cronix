package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/cli/config"
)

type globalStatusOpts struct {
	configPath        string
	filterNames       []string
	withHistory       bool
	historySince      time.Duration
	strict            bool
	parallel          int
	perBackendTimeout time.Duration
	output            string
	secretRefs        []string
}

func newGlobalStatusCmd() *cobra.Command {
	var opts globalStatusOpts
	cmd := &cobra.Command{
		Use:   "global-status",
		Short: "List cronix-owned entries across every configured backend",
		Long: `global-status reads the operator config (~/.cronix/config.yaml or
/etc/cronix/config.yaml) and queries List() on every configured backend
in parallel. It is a read-only, stateless aggregator — the backends remain
the source of truth.

This command does not compare against a manifest (that is ` + "`cronix drift`" + `).
It only answers: "what does cronix currently own on this host?"`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := opts.configPath
			if path == "" {
				path = config.ResolvedPath()
			}
			if path == "" {
				return fmt.Errorf("no config found in %v — create one and re-run, or pass --config", config.DefaultPaths())
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			results := runGlobalStatus(cmd.Context(), cfg, opts)
			if err := printGlobalStatus(cmd, opts, results); err != nil {
				return err
			}
			if opts.strict {
				for _, r := range results {
					if r.Err != nil {
						return fmt.Errorf("global-status: %d backend(s) errored (--strict)", countErrors(results))
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.configPath, "config", "", "path to backends config (default: ~/.cronix/config.yaml or /etc/cronix/config.yaml)")
	cmd.Flags().StringSliceVar(&opts.filterNames, "backend", nil, "limit to named entries from the config (repeatable)")
	cmd.Flags().BoolVar(&opts.withHistory, "with-history", false, "include LAST_FIRE/LAST_STATUS columns (one History call per entry; slow)")
	cmd.Flags().DurationVar(&opts.historySince, "history-since", 24*time.Hour, "lookback window for --with-history")
	cmd.Flags().BoolVar(&opts.strict, "strict", false, "exit non-zero if any backend errors")
	cmd.Flags().IntVar(&opts.parallel, "parallel", 4, "concurrent backend queries")
	cmd.Flags().DurationVar(&opts.perBackendTimeout, "per-backend-timeout", 30*time.Second, "deadline for each backend's List + History calls")
	cmd.Flags().StringSliceVar(&opts.secretRefs, "secret-ref", nil, "secret_refs forwarded to backends that require them (kubernetes, aws-scheduler)")
	cmd.Flags().StringVarP(&opts.output, "output", "o", "table", "output format: table|json")
	return cmd
}

// queryResult holds one backend's per-invocation data.
type queryResult struct {
	Entry   config.BackendEntry
	Items   []backends.ManagedEntry
	History map[string]backends.HistoryEntry // key: app+"."+job
	Err     error
}

func runGlobalStatus(ctx context.Context, cfg *config.Config, opts globalStatusOpts) []queryResult {
	filter := nameFilter(opts.filterNames)
	results := make([]queryResult, 0, len(cfg.Backends))
	resultsMu := sync.Mutex{}
	parallel := opts.parallel
	if parallel < 1 {
		parallel = 1
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, e := range cfg.Backends {
		if !filter(e.Name) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e config.BackendEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			r := queryOne(ctx, e, opts)
			resultsMu.Lock()
			results = append(results, r)
			resultsMu.Unlock()
		}(e)
	}
	wg.Wait()
	// Stable order: by config.Backends index so output matches the file.
	order := make(map[string]int, len(cfg.Backends))
	for i, e := range cfg.Backends {
		order[e.Name] = i
	}
	sort.SliceStable(results, func(i, j int) bool {
		return order[results[i].Entry.Name] < order[results[j].Entry.Name]
	})
	return results
}

func queryOne(ctx context.Context, e config.BackendEntry, opts globalStatusOpts) queryResult {
	r := queryResult{Entry: e}
	bctx, cancel := context.WithTimeout(ctx, opts.perBackendTimeout)
	defer cancel()
	b, err := BuildBackendFromEntry(e, opts.secretRefs)
	if err != nil {
		r.Err = fmt.Errorf("build: %w", err)
		return r
	}
	items, err := b.List(bctx)
	if err != nil {
		r.Err = fmt.Errorf("list: %w", err)
		return r
	}
	r.Items = items
	if opts.withHistory && len(items) > 0 {
		r.History = make(map[string]backends.HistoryEntry, len(items))
		since := time.Now().Add(-opts.historySince)
		for _, it := range items {
			h, err := b.History(bctx, backends.HistoryOpts{
				App: it.App, Job: it.Job, Since: since, Limit: 1,
			})
			if err != nil || len(h) == 0 {
				continue
			}
			r.History[it.App+"."+it.Job] = h[len(h)-1]
		}
	}
	return r
}

func nameFilter(names []string) func(string) bool {
	if len(names) == 0 {
		return func(string) bool { return true }
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(n string) bool { _, ok := set[n]; return ok }
}

func countErrors(rs []queryResult) int {
	n := 0
	for _, r := range rs {
		if r.Err != nil {
			n++
		}
	}
	return n
}

// JSON output shape — grouped by backend entry, no opaque Raw.
type globalStatusReport struct {
	Backends []backendReport `json:"backends"`
}

type backendReport struct {
	Name    string        `json:"name"`
	Type    string        `json:"type"`
	Error   string        `json:"error,omitempty"`
	Entries []entryReport `json:"entries"`
}

type entryReport struct {
	App        string `json:"app"`
	Job        string `json:"job"`
	Index      int    `json:"index"`
	Hash       string `json:"hash"`
	LastFire   string `json:"last_fire,omitempty"`
	LastStatus string `json:"last_status,omitempty"`
}

func printGlobalStatus(cmd *cobra.Command, opts globalStatusOpts, results []queryResult) error {
	switch opts.output {
	case "json":
		rep := globalStatusReport{Backends: make([]backendReport, 0, len(results))}
		for _, r := range results {
			br := backendReport{Name: r.Entry.Name, Type: r.Entry.Type}
			if r.Err != nil {
				br.Error = r.Err.Error()
			}
			for _, it := range r.Items {
				er := entryReport{App: it.App, Job: it.Job, Index: it.Index, Hash: it.Hash}
				if h, ok := r.History[it.App+"."+it.Job]; ok {
					er.LastFire = h.FinishedAt.UTC().Format(time.RFC3339)
					er.LastStatus = h.Status
				}
				br.Entries = append(br.Entries, er)
			}
			rep.Backends = append(rep.Backends, br)
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	default:
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		header := "BACKEND\tTYPE\tAPP\tJOB\tIDX\tHASH\tSTATUS"
		if opts.withHistory {
			header += "\tLAST_FIRE\tLAST_STATUS"
		}
		fmt.Fprintln(w, header)
		for _, r := range results {
			if r.Err != nil {
				row := fmt.Sprintf("%s\t%s\t-\t-\t-\t-\tERROR: %s", r.Entry.Name, r.Entry.Type, r.Err.Error())
				if opts.withHistory {
					row += "\t-\t-"
				}
				fmt.Fprintln(w, row)
				continue
			}
			if len(r.Items) == 0 {
				row := fmt.Sprintf("%s\t%s\t-\t-\t-\t-\tEMPTY", r.Entry.Name, r.Entry.Type)
				if opts.withHistory {
					row += "\t-\t-"
				}
				fmt.Fprintln(w, row)
				continue
			}
			for _, it := range r.Items {
				row := fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s\tOK", r.Entry.Name, r.Entry.Type, it.App, it.Job, it.Index, short(it.Hash))
				if opts.withHistory {
					if h, ok := r.History[it.App+"."+it.Job]; ok {
						row += "\t" + h.FinishedAt.UTC().Format(time.RFC3339) + "\t" + h.Status
					} else {
						row += "\t-\t-"
					}
				}
				fmt.Fprintln(w, row)
			}
		}
		return w.Flush()
	}
}
