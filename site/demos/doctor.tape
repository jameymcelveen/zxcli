Output doctor.gif
Set Shell "zsh"
Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1100
Set Height 700
Set Padding 24
Set TypingSpeed 60ms
Hide
Type "cd /tmp/demo/qiklog && export PS1='$ ' && clear"
Enter
Sleep 500ms
Show
Type "zx doctor"
Sleep 400ms
Enter
Wait+Screen@60s /attention|checks out|approved/
Sleep 4s
