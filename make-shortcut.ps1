$ws = New-Object -ComObject WScript.Shell
$lnkPath = [IO.Path]::Combine($env:USERPROFILE, 'Desktop', 'Kimi Web.lnk')
$sc = $ws.CreateShortcut($lnkPath)
$sc.TargetPath = 'D:\kimiweb\dist\KimiWeb.exe'
$sc.WorkingDirectory = 'D:\kimiweb\dist'
$sc.Description = 'Kimi Web Desktop'
$sc.Save()
Write-Output "shortcut created: $lnkPath"
