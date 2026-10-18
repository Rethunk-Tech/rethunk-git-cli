package gitx

import (
	"strings"
	"testing"
)

func TestParseLsTreeLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		line    string
		want    LsTreeEntry
		wantErr bool
	}{
		{
			name: "blob",
			line: "100644 blob abcdef0123456789	path/to/file.go",
			want: LsTreeEntry{Mode: "100644", Type: "blob", SHA: "abcdef0123456789", Path: "path/to/file.go"},
		},
		{
			name: "path may contain a tab-free space",
			line: "100644 blob abcdef0123456789	path with spaces.go",
			want: LsTreeEntry{Mode: "100644", Type: "blob", SHA: "abcdef0123456789", Path: "path with spaces.go"},
		},
		{
			name:    "no tab",
			line:    "100644 blob abcdef0123456789 path/to/file.go",
			wantErr: true,
		},
		{
			name:    "wrong field count before tab",
			line:    "100644 blob	path/to/file.go",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseLsTreeLine(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseLsTreeLine(%q) = %+v, nil; want an error", tt.line, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLsTreeLine(%q) = %v", tt.line, err)
			}
			if got != tt.want {
				t.Errorf("parseLsTreeLine(%q) = %+v; want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseNumstat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    []NumstatEntry
		wantErr bool
	}{
		{name: "empty", in: "", want: nil},
		{
			name: "one file",
			in:   "1\t2\ta.go\n",
			want: []NumstatEntry{{Added: "1", Deleted: "2", Path: "a.go"}},
		},
		{
			name: "binary counts stay dashes; rename shorthand stays in the path field",
			in:   "-\t-\told.go => new.go\n",
			want: []NumstatEntry{{Added: "-", Deleted: "-", Path: "old.go => new.go"}},
		},
		{
			name:    "two fields is malformed",
			in:      "1\ta.go\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseNumstat([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseNumstat(%q) = %+v, nil; want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNumstat(%q) = %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseNumstat(%q) len = %d; want %d (%+v)", tt.in, len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseNumstat(%q)[%d] = %+v; want %+v", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseCommitSummaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    []CommitSummary
		wantErr bool
	}{
		{name: "empty", in: "", want: nil},
		{
			name: "hash and subject",
			in:   "abcdef0123456789\tfeat(cli): add flag\n",
			want: []CommitSummary{{Hash: "abcdef0123456789", Subject: "feat(cli): add flag"}},
		},
		{
			name: "subject may contain further tabs",
			in:   "abcdef0123456789\tsubject\twith\ttabs\n",
			want: []CommitSummary{{Hash: "abcdef0123456789", Subject: "subject\twith\ttabs"}},
		},
		{
			name:    "no tab is malformed, not a hash with empty subject",
			in:      "abcdef0123456789 only\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCommitSummaries([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCommitSummaries(%q) = %+v, nil; want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCommitSummaries(%q) = %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseCommitSummaries(%q) len = %d; want %d (%+v)", tt.in, len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseCommitSummaries(%q)[%d] = %+v; want %+v", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadCatFileBatchResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		count   int
		want    []BatchCatFileResult
		wantErr bool
	}{
		{
			name:  "blob then missing",
			in:    "abc blob 4\nhey\n\nHEAD:gone.go missing\n",
			count: 2,
			want: []BatchCatFileResult{
				{Content: []byte("hey\n"), Exists: true},
				{Exists: false},
			},
		},
		{
			name:  "missing object name may contain spaces",
			in:    "HEAD:path with spaces.go missing\n",
			count: 1,
			want:  []BatchCatFileResult{{Exists: false}},
		},
		{
			name:  "submodule has no content bytes",
			in:    "0123456789abcdef submodule\n",
			count: 1,
			want:  []BatchCatFileResult{{Exists: false}},
		},
		{
			name:    "malformed header",
			in:      "not-a-header\n",
			count:   1,
			wantErr: true,
		},
		{
			name:    "negative size",
			in:      "abc blob -1\n",
			count:   1,
			wantErr: true,
		},
		{
			name:    "short read",
			in:      "",
			count:   1,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := readCatFileBatchResponses(strings.NewReader(tt.in), tt.count)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readCatFileBatchResponses(%q) = %+v, nil; want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readCatFileBatchResponses(%q) = %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d; want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i].Exists != tt.want[i].Exists || string(got[i].Content) != string(tt.want[i].Content) {
					t.Errorf("[%d] = {Exists:%v Content:%q}; want {Exists:%v Content:%q}",
						i, got[i].Exists, got[i].Content, tt.want[i].Exists, tt.want[i].Content)
				}
			}
		})
	}
}
