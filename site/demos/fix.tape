Output fix.gif
Set Shell "zsh"
Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1100
Set Height 700
Set Padding 24
Set TypingSpeed 60ms
Hide
Type "cd /tmp/demo2/qiklog && zx doctor --trust >/dev/null 2>&1 && export PS1='$ ' && clear"
Enter
Sleep 500ms
Show
Type "zx doctor --fix"
Sleep 400ms
Enter
Wait+Screen@60s /\[y\/N\]/
Sleep 1500ms
Type "y"
Enter
Wait+Screen@90s /need attention|checks out/
Sleep 2500ms
Hide
Type "clear && cat ~/dev/zxcli/site/demos/card.txt"
Enter
Show
Sleep 3s
Hide
Type "clear"
Enter
Show
Type "zx doctor"
Sleep 400ms
Enter
Wait+Screen@90s /need attention|checks out/
Sleep 4s
