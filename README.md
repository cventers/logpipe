logpipe
=======

STDIN to UNIX-domain socket log writer with prefix and reconnect support, written in Go

My use-case for logpipe is to serve as a destination for Apache logs:

ErrorLog "|/usr/local/bin/logpipe -socket /tmp/syslog-ng-apache-in.sock"

You could do something similar with socat or a similar utility, but I want
an easy way to add prefixes to log lines before they reach syslog-ng, and
I don't want to redefine Apache's log formats.

Plus I wanted to write some Go.
