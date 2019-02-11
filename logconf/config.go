package logconf

import (
	"fmt"
	"io"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const (
	logToConsoleCfgFieldName = "log.console"
	logToFileCfgFieldName    = "log.file"
)

// Initializes logger for Frozy components by reading values in common format
// from viper globals.
func FrozyInitLogging() (*log.Logger, error) {
	logFormatter := new(OutputFormatter)

	if viper.IsSet(logToConsoleCfgFieldName) {
		var outputFormat OutputFormat

		switch viper.GetString(logToConsoleCfgFieldName + ".format") {
		case "text":
			outputFormat = Text
		case "json":
			outputFormat = Json
		default:
			return nil, fmt.Errorf("Invalid output format field detected in user input for console logging settings")
		}

		logLevel, err := log.ParseLevel(viper.GetString(logToConsoleCfgFieldName + ".level"))
		if err != nil {
			return nil, fmt.Errorf("Invalid level format field detected in user input for console logging settings, %v", err)
		}

		err = logFormatter.AddWriter(&WriterData{
			Writer:       io.Writer(os.Stdout),
			WriterType:   ConsoleLog,
			OutputFormat: outputFormat,
			Level:        logLevel})
		if err != nil {
			return nil, fmt.Errorf("Failed to add console writer to logging subsystem due to: %s", err)
		}
	}

	if viper.IsSet(logToFileCfgFieldName) {
		var outputFormat OutputFormat

		switch viper.GetString(logToFileCfgFieldName + ".format") {
		case "text":
			outputFormat = Text
		case "json":
			outputFormat = Json
		default:
			return nil, fmt.Errorf("Invalid output format field detected in user input for external file logging settings")
		}

		logLevel, err := log.ParseLevel(viper.GetString(logToFileCfgFieldName + ".level"))
		if err != nil {
			return nil, fmt.Errorf("Invalid level format field detected in user input for file logging settings, %v", err)
		}

		logFile, err := os.OpenFile(viper.GetString(logToFileCfgFieldName+".path"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("Failed to create log file: %s", err)
		}

		_, err = logFile.WriteString("\n***************** NEW SYSLOG CHUNK DELIMITER **********************\n")
		if err != nil {
			return nil, fmt.Errorf("Failed to write delimiter message to logging file due to: %s", err)
		}

		err = logFormatter.AddWriter(&WriterData{
			Writer:       io.Writer(logFile),
			WriterType:   FileLog,
			OutputFormat: outputFormat,
			Level:        logLevel})
		if err != nil {
			return nil, fmt.Errorf("Failed to add log file writer to logging subsystem due to: %s", err)
		}

	}

	result := LoggerNew()
	// Set our own formatter
	// All formatter properties must be defined prior Formatter will be added to Logger
	if err := logFormatter.AddFormatterToLogger(result); err != nil {
		return nil, fmt.Errorf("Failed to add new Formatter to logging subsystem due to: %s", err)
	}

	return result, nil
}
