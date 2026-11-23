. "$PSScriptRoot/common.ps1"
Push-Location $Root
try {
    Invoke-Checked docker @('compose','ps')
    Invoke-Checked docker @('compose','exec','-T','ai-monitor-server','/app/ai-watch-check-api','--url','http://127.0.0.1:8765','--token-file','/app/config.local.json')
} finally { Pop-Location }
