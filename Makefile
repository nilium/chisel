PREFIX ?= /usr/local
BIN_PREFIX ?= ${PREFIX}/bin
MAN_PREFIX ?= ${PREFIX}/share/man

# Commands to build:
COMMANDS =
COMMANDS += go.spiff.io/chisel

# Manpages to install:
MANPAGES =

GO_TAGS ?=

SQLITE_DEFAULT_OPTIONS += sqlite_json
SQLITE_DEFAULT_OPTIONS += sqlite_fts5
SQLITE_DEFAULT_OPTIONS += sqlite_icu
SQLITE_DEFAULT_OPTIONS += sqlite_omit_load_extension
SQLITE_OPTIONS ?= ${SQLITE_DEFAULT_OPTIONS}
GO_TAGS += ${SQLITE_OPTIONS}

EXES =

all:: exe man

exe::
man::

install-exe::
install-man::
install:: install-exe install-man

# Build and install Go commands.
.for cmd in ${COMMANDS}
prog_${cmd} = bin/${cmd:T}
EXES += ${prog_${cmd}}

${prog_${cmd}}::
	go build -tags ${GO_TAGS:ts,:Q} -v -o ${.TARGET:Q} ${cmd:Q}

exe:: ${prog_${cmd}}

${BIN_PREFIX}/${cmd:T}: ${prog_${cmd}}
	${INSTALL} -TD ${prog_${cmd}:Q} ${.TARGET:Q}

install-exe:: ${BIN_PREFIX}/${cmd:T}
.endfor

# Build and install man pages.
.for man in ${MANPAGES}

${man}: ${man}.scd
	scdoc <${man}.scd >${man}

man:: ${man}

${MAN_PREFIX}/man${man:E}/${man}: ${man}
	${INSTALL} -TD ${man:Q} ${.TARGET:Q}

install-man:: ${MAN_PREFIX}/man${man:E}/${man}
.endfor

clean::
	rm -f ${EXES} ${MANPAGES}
