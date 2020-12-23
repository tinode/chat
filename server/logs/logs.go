/******************************************************************************
 *
 *  Description :
 *    Package exposes info, warning and error loggers.
 *
 *****************************************************************************/
package logs

import (
	"log"
	"os"
)

var (
    Info    *log.Logger
    Warning *log.Logger
    Error   *log.Logger
)

func Init() {
	Info = log.New(os.Stdout, "I", log.LstdFlags | log.Lshortfile)
	Warning = log.New(os.Stdout, "W", log.LstdFlags | log.Lshortfile)
	Error = log.New(os.Stdout, "E", log.LstdFlags | log.Lshortfile)
}
