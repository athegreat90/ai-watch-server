. "$PSScriptRoot/common.ps1"
Push-Location $Root
try {
    Invoke-Checked docker @('compose','ps')
    Invoke-Checked python @('bridge/check_api.py')
} finally { Pop-Location }
