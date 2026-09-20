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
rem Node side. The copy uses ordinary percent expansion inside a fully quoted set
rem assignment, with delayed expansion left OFF on purpose: under setlocal
rem EnableDelayedExpansion the assignment's own result is scanned a second time,
rem so an exclamation mark or a caret in the text would be eaten -- reintroducing
rem the exact class of silent corruption this change removes, on a different
rem character. The variable name is built with ordinary expansion of the counter
rem rather than delayed expansion because a batch file re-parses each line every
rem time a goto returns to it, so the counter's current value is already visible.
rem
rem The tilde form of the argument strips the caller's surrounding quotes, so the
rem variable holds the argument's value rather than its quoted spelling. shift
rem walks the whole list, so there is no ten-argument limit. With no arguments at
rem all, GRAPH_OPS_ARGC is 0 and cmd-args.js returns an empty argv.
rem
rem Known limitation: cmd.exe cannot store an empty string in an environment
rem variable (assigning nothing deletes it), so an empty argument is not
rem representable. cmd-args.js then sees a missing variable, returns null, and the
rem shim falls back to process.argv rather than silently dropping arguments.
setlocal
set GRAPH_OPS_ARGC=0
:graph_ops_next_arg
if "%1"=="" goto graph_ops_run
set /a GRAPH_OPS_ARGC+=1
set "GRAPH_OPS_ARG_%GRAPH_OPS_ARGC%=%~1"
shift
goto graph_ops_next_arg
:graph_ops_run
node "%~dp0..\scripts\engine-shim.js"
endlocal & exit /b %ERRORLEVEL%
