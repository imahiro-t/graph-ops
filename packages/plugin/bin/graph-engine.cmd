@echo off
rem graph-engine shim for cmd.exe and PowerShell, the Windows counterpart of
rem bin/graph-engine. Claude Code adds this plugin's bin/ to the Bash tool's
rem PATH, so commands run a bare graph-engine; on Windows that resolves here.
rem
rem Comments here use no ampersand, pipe or redirection character on purpose:
rem cmd.exe splits a line on those before rem ever runs, so one inside a comment
rem would execute as a command.
rem
rem Arguments are NOT forwarded with the all-arguments token (finding CHK-05,
rem BatBadBut class): whatever that token expands to is re-scanned by cmd.exe, so
rem an argument containing a percent-delimited variable name, an ampersand or a
rem quote gets expanded, split, or turned into a second command. That matters
rem here because graph-engine's arguments are arbitrary prose -- add-artifact
rem puts a whole document on the command line as its last argument.
rem
rem Instead, each argument cmd.exe has ALREADY finished splitting is copied into
rem its own environment variable and reassembled by scripts/cmd-args.js on the
rem Node side. Three rules keep that copy from re-opening the hole it closes,
rem and packages/plugin/test/bin-files.test.js pins all three, because there is
rem no cmd.exe on the machines that run our tests:
rem
rem 1. A parameter is only ever read through its tilde form, which strips the
rem    caller's surrounding quotes, and the result is only ever placed INSIDE a
rem    pair of quotes this file writes. Reading a parameter bare would paste the
rem    caller's own quotes into the line: they close the quoting immediately,
rem    which puts the argument's text -- ampersands included -- back out in the
rem    open where cmd.exe splits on it. That is the same injection as the
rem    all-arguments token, one line later.
rem 2. Delayed expansion stays OFF. With it on, the result of an expansion is
rem    scanned a second time, so an exclamation mark or a caret in the text
rem    would be eaten -- the same silent corruption of user text on different
rem    characters. The variable name is built with ordinary expansion of the
rem    counter instead, which works because a batch file re-parses each line
rem    every time a goto returns to it, so the counter's current value is
rem    already visible.
rem 3. This file's own location is resolved BEFORE the loop and kept in a
rem    variable. shift renumbers the batch parameters, and the parameter
rem    holding this file's path is one of them, so a path built from it after
rem    the loop points at nothing: every invocation that has arguments would
rem    fail to find engine-shim.js. shift /1 (shift from parameter one) leaves
rem    that parameter alone as well -- both guards are one word each.
rem
rem Known limitations, both inherent to batch and neither of them new here:
rem
rem - An argument whose own text contains a quote can still flip the quoting of
rem   the line that copies it, because that line is one pair of quotes and the
rem   argument's quote lands between them. An argument typed without surrounding
rem   quotes, whose text is a quote followed by an ampersand, a command name,
rem   another ampersand and a closing quote, is the case: cmd.exe hands it over
rem   unchanged, and the assignment's closing quote is then no longer where it
rem   was written. No form of batch assignment is immune to this, so it is
rem   recorded here as a limitation rather than claimed to be solved. It affects
rem   Windows only, and nothing that calls graph-engine builds such arguments.
rem - cmd.exe cannot store an empty string in an environment variable, so an
rem   empty argument cannot be relayed and cannot be told apart from the end of
rem   the list -- both make the tilde form expand to nothing. When one is
rem   followed by further arguments the loop would end early and quietly run a
rem   PREFIX of what the user typed, so the end of the loop looks one position
rem   ahead: if anything follows, the count is replaced with a word that
rem   cmd-args.js refuses to parse, and the shim reports the limitation instead
rem   of running a different command. A trailing empty argument is dropped, and
rem   graph-engine reports the missing argument itself. Pass such a value on
rem   stdin (the - form) instead.
setlocal
set "GRAPH_OPS_SHIM=%~dp0..\scripts\engine-shim.js"
set GRAPH_OPS_ARGC=0
:graph_ops_next_arg
rem The termination test goes through a variable rather than comparing the
rem argument against an empty string in place: a quote inside the argument's
rem own text then lands in an assignment, which absorbs it, instead of in an
rem if comparison, where it would split the condition into a syntax error. It
rem cannot distinguish an empty argument from the end of the list either way
rem (see the limitations above); :graph_ops_end_of_args is what handles that.
set "GRAPH_OPS_ARG_PEEK=%~1"
if not defined GRAPH_OPS_ARG_PEEK goto graph_ops_end_of_args
set /a GRAPH_OPS_ARGC+=1
set "GRAPH_OPS_ARG_%GRAPH_OPS_ARGC%=%~1"
shift /1
goto graph_ops_next_arg
:graph_ops_end_of_args
rem Either the list ended, or the argument at this position was an empty
rem string. Look one position further: if something is there, the list did not
rem end, so refuse to run rather than run a prefix of it. The peek variable
rem starts with the relay's own prefix, so cmd-args.js strips it along with the
rem rest and it cannot reach graph-engine.
shift /1
set "GRAPH_OPS_ARG_PEEK=%~1"
if defined GRAPH_OPS_ARG_PEEK set "GRAPH_OPS_ARGC=empty-argument"
node "%GRAPH_OPS_SHIM%"
endlocal & exit /b %ERRORLEVEL%
