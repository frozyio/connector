package logconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
)

const defaultTimestampFormat = "2006-01-02 15:04:05"

const (
	nocolor = 0
	red     = 31
	green   = 32
	yellow  = 33
	blue    = 36
	gray    = 37
)

// Types of log writers
const (
	ConsoleLog = iota
	FileLog
)

// Types of log output formats
const (
	Text = iota
	Json
)

// WriterType stores Writer type (Console, File, RSysLog and so on)
type WriterType uint64

// OutputFormat stores Output type (text, json and so on)
type OutputFormat uint64

// WriterData stores information about registered writers
type WriterData struct {
	Writer io.Writer
	WriterType
	OutputFormat
	Level log.Level
}

// TextFormatter formats logs into text
type OutputFormatter struct {
	isFormatterLocked bool

	// List of registered writers for that formatter
	// Depending on Writer type, formatter will do some separation in output
	// ATTENTION! It's not a threadsafe operation to change list of Writers in Runtime!
	// so, this will work until forwarder is added to logger
	out []*WriterData

	// internal multiFormatter
	mf multiFormatter
}

// AddWriter adds new writer to formatter
// ATTENTION! It's not a threadsafe operation to change list of Writers in Runtime!
func (f *OutputFormatter) AddWriter(writer *WriterData) error {
	if f.isFormatterLocked {
		return errors.New("Can't register writer due to formatter is already used by logger")
	}

	if writer == nil {
		return errors.New("Can't register empty writer")
	}

	switch writer.WriterType {
	case ConsoleLog, FileLog:
	default:
		return errors.New("Can't register writer with unknown type")
	}

	switch writer.OutputFormat {
	case Text, Json:
	default:
		return errors.New("Can't register writer with unknown output format")
	}

	f.out = append(f.out, writer)

	return nil
}

// AddFormatterToLogger adds new formatter to logger
func (f *OutputFormatter) AddFormatterToLogger(logger *log.Logger) error {
	if logger == nil {
		return errors.New("Can't register formatter in empty logger")
	}

	f.isFormatterLocked = true

	logger.SetFormatter(f)

	return nil
}

func (f *OutputFormatter) Format(entry *log.Entry) ([]byte, error) {
	// If we doesn't have registered writers, exit immediatly
	if len(f.out) == 0 {
		return nil, nil
	}

	// go through all writers and get their own formatters
	for _, writerVal := range f.out {
		// skip invalid levels
		if writerVal.Level < entry.Level {
			continue
		}

		var b *bytes.Buffer

		if entry.Buffer != nil {
			b = entry.Buffer
		} else {
			b = &bytes.Buffer{}
		}

		// after buffer prepared
		switch writerVal.OutputFormat {
		case Text:
			// invoke TextFormatter Format method
			f.mf.textFormat(entry, b, writerVal)
		case Json:
			// invoke JsonFormatter Format method
			err := f.mf.jsonFormat(entry, b)
			if err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("Uknown output format detected")
		}

		_, err := writerVal.Writer.Write(b.Bytes())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write to log, %v\n", err)
		}

		// drop buffer data
		b.Reset()
	}

	return nil, nil
}

// TextFormatter formats logs into text
type multiFormatter struct {
	// The fields are sorted by default for a consistent output. For applications
	// that log extremely frequently and don't use the JSON formatter this may not
	// be desired.
	disableSorting bool

	// Force disabling colors.
	disableColors bool

	// Disable timestamp logging. useful when output is redirected to logging
	// system that already adds timestamps.
	disableTimestamp bool

	// TimestampFormat to use for display
	timestampFormat string

	// Disables the truncation of the level text to 4 characters.
	disableLevelTruncation bool

	// QuoteEmptyFields will wrap empty fields in quotes if true
	quoteEmptyFields bool

	// DataKey allows users to put all the log entry parameters into a nested dictionary at a given key.
	DataKey string

	// PrettyPrint will indent all json logs
	PrettyPrint bool
}

// Format renders a single log entry
func (f *multiFormatter) jsonFormat(entry *log.Entry, buffer *bytes.Buffer) error {
	data := make(log.Fields, len(entry.Data)+3)
	for k, v := range entry.Data {
		switch v := v.(type) {
		case error:
			// Otherwise errors are ignored by `encoding/json`
			// https://github.com/sirupsen/logrus/issues/137
			data[k] = v.Error()
		default:
			data[k] = v
		}
	}

	if f.DataKey != "" {
		newData := make(log.Fields, 4)
		newData[f.DataKey] = data
		data = newData
	}

	timestampFormat := f.timestampFormat
	if timestampFormat == "" {
		timestampFormat = defaultTimestampFormat
	}

	if !f.disableTimestamp {
		data["time"] = entry.Time.Format(timestampFormat)
	}
	data["msg"] = entry.Message
	data["level"] = entry.Level.String()

	encoder := json.NewEncoder(buffer)
	if f.PrettyPrint {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("Failed to marshal fields to JSON, %v", err)
	}

	return nil
}

// Format renders a single log entry
func (f *multiFormatter) textFormat(entry *log.Entry, buffer *bytes.Buffer, writerVal *WriterData) {
	keys := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		keys = append(keys, k)
	}

	if !f.disableSorting {
		sort.Strings(keys)
	}

	timestampFormat := f.timestampFormat
	if timestampFormat == "" {
		timestampFormat = defaultTimestampFormat
	}

	if !f.disableColors && (writerVal.WriterType == ConsoleLog) {
		f.printColored(buffer, entry, keys, timestampFormat)
	} else {
		// in this case we output log level
		if (writerVal.WriterType == ConsoleLog) || (writerVal.WriterType == FileLog) {
			levelText := strings.ToUpper(entry.Level.String())

			if !f.disableLevelTruncation {
				levelText = levelText[0:4]
			}

			fmt.Fprintf(buffer, "%s", levelText)
		}

		if f.disableTimestamp {
			fmt.Fprintf(buffer, " %-44s ", entry.Message)
		} else {
			fmt.Fprintf(buffer, "[%s] %-44s ", entry.Time.Format(timestampFormat), entry.Message)
		}

		for _, key := range keys {
			f.appendKeyValue(buffer, key, entry.Data[key])
		}
	}

	buffer.WriteByte('\n')
}

func (f *multiFormatter) printColored(b *bytes.Buffer, entry *log.Entry, keys []string, timestampFormat string) {
	var levelColor int

	switch entry.Level {
	case log.DebugLevel:
		levelColor = gray
	case log.WarnLevel:
		levelColor = yellow
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		levelColor = red
	default:
		levelColor = blue
	}

	levelText := strings.ToUpper(entry.Level.String())

	if !f.disableLevelTruncation {
		levelText = levelText[0:4]
	}

	// Remove a single newline if it already exists in the message to keep
	// the behavior of logrus text_formatter the same as the stdlib log package
	entry.Message = strings.TrimSuffix(entry.Message, "\n")

	if f.disableTimestamp {
		fmt.Fprintf(b, "\x1b[%dm%s\x1b[0m %-44s ", levelColor, levelText, entry.Message)
	} else {
		fmt.Fprintf(b, "\x1b[%dm%s\x1b[0m[%s] %-44s ", levelColor, levelText, entry.Time.Format(timestampFormat), entry.Message)
	}

	for _, k := range keys {
		v := entry.Data[k]
		fmt.Fprintf(b, " \x1b[%dm%s\x1b[0m=", levelColor, k)
		f.appendValue(b, v)
	}
}

func (f *multiFormatter) needsQuoting(text string) bool {
	if f.quoteEmptyFields && len(text) == 0 {
		return true
	}

	for _, ch := range text {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '.' || ch == '_' || ch == '/' || ch == '@' || ch == '^' || ch == '+') {
			return true
		}
	}

	return false
}

func (f *multiFormatter) appendKeyValue(b *bytes.Buffer, key string, value interface{}) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(key)
	b.WriteByte('=')
	f.appendValue(b, value)
}

func (f *multiFormatter) appendValue(b *bytes.Buffer, value interface{}) {
	stringVal, ok := value.(string)
	if !ok {
		stringVal = fmt.Sprint(value)
	}

	if !f.needsQuoting(stringVal) {
		b.WriteString(stringVal)
	} else {
		b.WriteString(fmt.Sprintf("%q", stringVal))
	}
}

//LoggerNew creates new Logger with disabled output
func LoggerNew() *log.Logger {
	// create new Logger and set their parameters
	logger := log.New()

	// Output to /dev/null by default instead of the default stderr
	// in our logger actual output will perform Formatter
	logger.SetOutput(ioutil.Discard)

	// We accept all levels. We will filter messages in out formatter
	logger.SetLevel(log.DebugLevel)

	return logger
}
