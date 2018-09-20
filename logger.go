package logger

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/Hatch1fy/errors"
	"github.com/PathDNA/atoms"
)

const (
	// ErrMessageContainsNewline is returned when a message contains a newline
	ErrMessageContainsNewline = errors.Error("message contains newline, which is not a valid character")
	// ErrInvalidRotationInterval is returned when a rotation interval is set to zero
	ErrInvalidRotationInterval = errors.Error("rotation interval cannot be zero")
)

const (
	// loggerFlag is the os file flags used for log files
	loggerFlag = os.O_RDWR | os.O_APPEND | os.O_CREATE
)

var (
	// newline as a byteslice
	newline = []byte("\n")
)

// New will return a new instance of Logger
func New(dir, name string) (lp *Logger, err error) {
	var l Logger
	l.dir = dir
	l.name = name

	// Set initial logger file
	if err = l.setFile(); err != nil {
		return
	}

	// Assign lp as a pointer to our created logger
	lp = &l
	return
}

// Logger will manage system logs
type Logger struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer

	// Log directory
	dir string
	// Log name
	name string

	// Number of lines before rotation (defaults to unlimited)
	numLines int
	// Duration before rotation (defaults to unlimited)
	rotateInterval time.Duration

	// Current line count
	count int

	// Closed state
	closed atoms.Bool
}

// isClosed will return the current closed state
// Note: This function is atomic
func (l *Logger) isClosed() (ok bool) {
	return l.closed.Get()
}

// setFile will set the underlying logger file
// Note: This will close the currently opened file
func (l *Logger) setFile() (err error) {
	// Close existing file (if it exists)
	if err = l.closeFile(); err != nil {
		return
	}

	// Open a file with our directory, name, and current timestamp
	if l.f, err = os.OpenFile(l.getFilename(), loggerFlag, 0644); err != nil {
		return
	}

	// Set writer
	l.w = bufio.NewWriter(l.f)
	// Reset count to zero
	l.count = 0
	return
}

// closeFile will close the underlying logger file
// Note: This will flush the buffer and file before closing
func (l *Logger) closeFile() (err error) {
	if l.f == nil {
		// File does not exist - no need to close, return
		return
	}

	if l.count == 0 {
		// This file is empty, let's clean it up when we're finished closing
		// Get name now to avoid calling a nil pointer later
		name := l.f.Name()
		// Defer the removal of the current file (this will allow the flushing and closing to complete)
		defer os.Remove(name)
	}

	// Flush contents
	if err = l.flush(); err != nil {
		return
	}

	// Close file
	if err = l.f.Close(); err != nil {
		return
	}

	// Set file to nil
	l.f = nil
	// Set buffer to nil
	l.w = nil
	return
}

// flush will flush the contents of the buffer and sync the underlying file
func (l *Logger) flush() (err error) {
	// Flush buffer
	if err = l.w.Flush(); err != nil {
		return
	}

	// Flush file
	return l.f.Sync()
}

func (l *Logger) rotationLoop() {
	var err error
	for {
		time.Sleep(l.rotateInterval)
		err = l.rotate()

		switch err {
		case nil:
		case errors.ErrIsClosed:
			return

		default:
			fmt.Printf("logger :: %s :: error rotating file: %v", l.name, err)
		}
	}
}

func (l *Logger) rotate() (err error) {
	// Acquire lock
	l.mu.Lock()
	// Defer the release of our lock
	defer l.mu.Unlock()

	// Ensure the logger has not been closed
	if l.isClosed() {
		// Instance of logger has been closed, return
		return errors.ErrIsClosed
	}

	if l.count == 0 {
		return
	}

	return l.setFile()
}

// getFilename will get the current full filename for the log
// Note: This function is time-sensitive (seconds)
func (l *Logger) getFilename() (filename string) {
	// Get current unix timestamp
	now := time.Now().UnixNano()
	// Create a filename by:
	//	- Concatinate directory and name
	//	- Append unix timestamp
	//	- Append log extension
	return fmt.Sprintf("%s.%d.log", path.Join(l.dir, l.name), now)
}

// logMessage will log the full message (prefix, message, suffix)
func (l *Logger) logMessage(msg []byte) (err error) {
	// Write timestamp
	if _, err = l.w.Write(getTimestampBytes()); err != nil {
		return
	}

	// Write '@', which separates timestamp and the message
	if err = l.w.WriteByte('@'); err != nil {
		return
	}

	// Write message
	if _, err = l.w.Write(msg); err != nil {
		return
	}

	// Write newline to follow message
	return l.w.WriteByte('\n')
}

// incrementCount will increment the current line count
// Note: If the line count exceeds the line limit, a new file will be set
func (l *Logger) incrementCount() (err error) {
	// Increment count, then ensure new count does not equal our number of lines limit
	if l.count++; l.numLines == 0 || l.count < l.numLines {
		// Line number limit unset OR count is less than our lines, return
		return
	}

	// Count equals our number of lines limit, set file
	return l.setFile()
}

// Log will log a message
func (l *Logger) Log(msg []byte) (err error) {
	// Ensure the message is valid before acquiring lock
	if bytes.Index(msg, newline) > -1 {
		// Log message contains a newline, return
		return ErrMessageContainsNewline
	}

	// Acquire lock
	l.mu.Lock()
	// Defer the release of our lock
	defer l.mu.Unlock()

	// Ensure the logger has not been closed
	if l.isClosed() {
		// Instance of logger has been closed, return
		return errors.ErrIsClosed
	}

	// Log message
	if err = l.logMessage(msg); err != nil {
		return
	}

	// Increment line count
	return l.incrementCount()
}

// LogString will log a string message
func (l *Logger) LogString(msg string) (err error) {
	// Convert message to bytes and pass to l.Log
	return l.Log([]byte(msg))
}

// Flush will manually flush the buffer bytes to disk
// Note: This is not typically needed, only needed in rare and/or debugging situations
func (l *Logger) Flush() (err error) {
	// Acquire lock
	l.mu.Lock()
	// Defer the release of our lock
	defer l.mu.Unlock()

	// Ensure the logger has not been closed
	if l.isClosed() {
		// Instance of logger has been closed, return
		return errors.ErrIsClosed
	}

	// Flush contents
	return l.flush()
}

// SetNumLines will set the maximum number of lines per log file
func (l *Logger) SetNumLines(n int) {
	// Acquire lock
	l.mu.Lock()
	// Defer the release of our lock
	defer l.mu.Unlock()
	// Set line number limit
	l.numLines = n
}

// SetRotateInterval will set the rotation interval timing of a log file
func (l *Logger) SetRotateInterval(duration time.Duration) (err error) {
	var wasUnset bool
	if duration == 0 {
		err = ErrInvalidRotationInterval
		return
	}

	// Acquire lock
	l.mu.Lock()
	// Defer the release of our lock
	defer l.mu.Unlock()

	// Ensure the logger has not been closed
	if l.isClosed() {
		// Instance of logger has been closed, return
		return errors.ErrIsClosed
	}

	// Set unset to true if rotate interval is currently zero
	wasUnset = l.rotateInterval == 0
	// Set rotate interval to the provided duration
	l.rotateInterval = duration

	if wasUnset {
		// Rotate interval was previously unset, initialize rotation loop
		go l.rotationLoop()
	}

	return
}

// Close will attempt to close an instance of logger
func (l *Logger) Close() (err error) {
	if !l.closed.Set(true) {
		return errors.ErrIsClosed
	}

	// Acquire lock to ensure all writers have completed
	l.mu.Lock()
	defer l.mu.Unlock()

	// Close underlying logger file
	return l.closeFile()
}
