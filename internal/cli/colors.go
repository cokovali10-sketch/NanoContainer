package cli

const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	Gray    = "\033[90m"
)

func Success(msg string) string {
	return Green + msg + Reset
}

func Error(msg string) string {
	return Red + msg + Reset
}

func Warning(msg string) string {
	return Yellow + msg + Reset
}

func Info(msg string) string {
	return Cyan + msg + Reset
}

func Dim(msg string) string {
	return Gray + msg + Reset
}

func BoldText(msg string) string {
	return Bold + msg + Reset
}
