package raft_test

import (
	"bytes"
	"log"
	"os"
	"reflect"
	"runtime"
	"testing"
	"testing/quick"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/influxdb/influxdb/raft"
)

// Ensure that a new log can be successfully opened and closed.
func TestLog_Open(t *testing.T) {
	l := NewUnopenedTestLog()
	if err := l.Open(tempfile()); err != nil {
		t.Fatal("open: ", err)
	} else if !l.Opened() {
		t.Fatal("expected log to be open")
	}
	if err := l.Close(); err != nil {
		t.Fatal("close: ", err)
	} else if l.Opened() {
		t.Fatal("expected log to be closed")
	}
}

// Ensure that a log can read entries from a stream.
func TestLog_ReadWrite(t *testing.T) {
	l := NewTestLog()
	defer l.Close()

	// Entries to write.
	entries := []*raft.LogEntry{
		&raft.LogEntry{Index: 1, Term: 1, Data: []byte{0}},
		&raft.LogEntry{Index: 2, Term: 1, Data: []byte{1}},
	}

	// Create reader.
	var w bytes.Buffer
	enc := raft.NewLogEntryEncoder(&w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatal("encode: ", err)
		}
	}

	// Consume from the reader.
	if err := l.ReadFrom(&BufferCloser{&w}); err != nil {
		t.Fatal("read from: ", err)
	}

	// Force an election.
	if err := l.Elect(); err != nil {
		t.Fatal("elect: ", err)
	}

	// Read entries back out of the log.
	var r bytes.Buffer
	go func() {
		if err := l.WriteTo(&BufferCloser{&r}, 0, 0); err != nil {
			t.Fatal("write to: ", err)
		}
	}()
	time.Sleep(10 * time.Millisecond) // HACK(benbjohnson)

	// Verify entries.
	dec := raft.NewLogEntryDecoder(&r)
	for _, exp := range entries {
		var e raft.LogEntry
		if err := dec.Decode(&e); err != nil {
			t.Fatal("decode(0): ", err)
		} else if !reflect.DeepEqual(exp, &e) {
			t.Fatalf("entry:\n\nexp: %#v\n\ngot: %#v\n\n", exp, &e)
		}
	}
}

// Ensure that log entries can be encoded to a writer.
func TestLogEntryEncoder_Encode(t *testing.T) {
	var buf bytes.Buffer

	// Encode the entry to the buffer.
	enc := raft.NewLogEntryEncoder(&buf)
	if err := enc.Encode(&raft.LogEntry{Type: raft.LogEntryNop, Index: 2, Term: 3, Data: []byte{4, 5, 6}}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// Check that the encoded bytes match what's expected.
	exp := []byte{0x10, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 3, 4, 5, 6}
	if v := buf.Bytes(); !bytes.Equal(exp, v) {
		t.Fatalf("value:\n\nexp: %x\n\ngot: %x\n\n", exp, v)
	}
}

// Ensure that log entries can be decoded from a reader.
func TestLogEntryDecoder_Decode(t *testing.T) {
	buf := bytes.NewBuffer([]byte{0x10, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 3, 4, 5, 6})

	// Create a blank entry and an expected result.
	entry := &raft.LogEntry{}

	// Decode the entry from the buffer.
	dec := raft.NewLogEntryDecoder(buf)
	if err := dec.Decode(entry); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if typ := raft.LogEntryNop; typ != entry.Type {
		t.Fatalf("type: exp: %v, got: %v", typ, entry.Type)
	}
	if index := uint64(2); index != entry.Index {
		t.Fatalf("index: exp: %v, got: %v", index, entry.Index)
	}
	if term := uint64(3); term != entry.Term {
		t.Fatalf("term: exp: %v, got: %v", term, entry.Term)
	}
	if data := []byte{4, 5, 6}; !bytes.Equal(data, entry.Data) {
		t.Fatalf("data: exp: %x, got: %x", data, entry.Term)
	}
}

// Ensure that random entries can be encoded and decoded correctly.
func TestLogEntryEncodeDecode(t *testing.T) {
	f := func(entries []raft.LogEntry) bool {
		var buf bytes.Buffer
		enc := raft.NewLogEntryEncoder(&buf)
		dec := raft.NewLogEntryDecoder(&buf)

		// Limit fields.
		for i := range entries {
			entries[i].Type = raft.LogEntryType(int(entries[i].Type) % 16)
		}

		// Encode entries.
		for _, e := range entries {
			if err := enc.Encode(&e); err != nil {
				t.Fatalf("encode: %s", err)
			}
		}

		// Decode entries.
		for _, e := range entries {
			var entry raft.LogEntry
			if err := dec.Decode(&entry); err != nil {
				t.Fatalf("decode: %s", err)
			} else if !reflect.DeepEqual(e, entry) {
				t.Fatalf("mismatch:\n\nexp: %#v\n\ngot: %#v\n\n", e, entry)
			}
		}

		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func BenchmarkLogEntryEncoderEncode_8b(b *testing.B)  { benchmarkLogEntryEncoderEncode(b, 8) }
func BenchmarkLogEntryEncoderEncode_32b(b *testing.B) { benchmarkLogEntryEncoderEncode(b, 32) }

func benchmarkLogEntryEncoderEncode(b *testing.B, sz int) {
	var buf bytes.Buffer
	enc := raft.NewLogEntryEncoder(&buf)
	entry := &raft.LogEntry{Data: make([]byte, sz)}

	// Record single encoding size.
	enc.Encode(entry)
	b.SetBytes(int64(buf.Len()))
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := enc.Encode(entry); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	runtime.GC()
}

func BenchmarkLogEntryDecoderDecode_8b(b *testing.B)  { benchmarkLogEntryDecoderDecode(b, 8) }
func BenchmarkLogEntryDecoderDecode_32b(b *testing.B) { benchmarkLogEntryDecoderDecode(b, 32) }

func benchmarkLogEntryDecoderDecode(b *testing.B, sz int) {
	var buf bytes.Buffer
	enc := raft.NewLogEntryEncoder(&buf)
	dec := raft.NewLogEntryDecoder(&buf)

	// Encode a single record and record its size.
	enc.Encode(&raft.LogEntry{Data: make([]byte, sz)})
	b.SetBytes(int64(buf.Len()))

	// Encode all the records on the buffer first.
	buf.Reset()
	for i := 0; i < b.N; i++ {
		if err := enc.Encode(&raft.LogEntry{Data: make([]byte, sz)}); err != nil {
			b.Fatalf("encode: %s", err)
		}
	}
	b.ReportAllocs()

	// Decode from the buffer.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var entry raft.LogEntry
		if err := dec.Decode(&entry); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	runtime.GC()
}

// TestLog wraps the raft.Log to provide helper test functions.
type TestLog struct {
	*raft.Log
}

// NewTestLog returns a new, opened instance of TestLog.
func NewTestLog() *TestLog {
	l := NewUnopenedTestLog()
	if err := l.Open(tempfile()); err != nil {
		log.Fatal("open: ", err)
	}
	return l
}

// NewUnopenedTestLog returns a new, unopened instance of TestLog.
// The log uses mock clock by default.
func NewUnopenedTestLog() *TestLog {
	l := &TestLog{
		Log: &raft.Log{
			Clock: clock.NewMockClock(),
		},
	}
	return l
}

// Close closes the log and removes the underlying data.
func (t *TestLog) Close() error {
	defer os.RemoveAll(t.Path())
	return t.Log.Close()
}

// BufferCloser represents a bytes.Buffer that provides a no-op close.
type BufferCloser struct {
	*bytes.Buffer
}

func (b *BufferCloser) Close() error { return nil }