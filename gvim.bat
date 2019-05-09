set /P a=x86? [y/N]
ghrdl --dir gvim_x64 --url https://github.com/vim/vim-win32-installer/releases --pattern "gvim_(?P<version>[0-9.]+)_x64.zip"
if %a%==y (
  ghrdl --dir gvim_x86 --url https://github.com/vim/vim-win32-installer/releases --pattern "gvim_(?P<version>[0-9.]+)_x86.zip"
)
