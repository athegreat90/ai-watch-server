. "$PSScriptRoot/common.ps1"
Push-Location $Root
try {
    Invoke-Checked python @('bridge/init_config.py')
    Invoke-Checked docker @('compose','config','--quiet')
    Invoke-Checked docker @('compose','build')
    Invoke-Checked docker @('compose','up','-d','--wait','--wait-timeout','90')
    Invoke-Checked docker @('compose','ps')
    Invoke-Checked python @('bridge/check_api.py')
} finally { Pop-Location }
