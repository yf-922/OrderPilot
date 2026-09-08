$ErrorActionPreference = 'Stop'
$base = if ($env:API_BASE_URL) { $env:API_BASE_URL } else { 'http://127.0.0.1:8080' }

function Assert-Status($actual, $expected, $name) {
  if ($actual -ne $expected) { throw "${name}: expected HTTP $expected, got $actual" }
}

$health = Invoke-WebRequest "$base/health" -UseBasicParsing
Assert-Status $health.StatusCode 200 'health'
$ready = Invoke-WebRequest "$base/ready" -UseBasicParsing
Assert-Status $ready.StatusCode 200 'ready'
if (-not $health.Headers['X-Request-ID']) { throw 'health response did not include X-Request-ID' }
if ($health.Headers['X-Content-Type-Options'] -ne 'nosniff') { throw 'security headers are missing' }

$unauthorized = try { Invoke-WebRequest "$base/product/1" -UseBasicParsing } catch { $_.Exception.Response }
if ($unauthorized.StatusCode.value__ -ne 401) { throw "auth: expected HTTP 401, got $($unauthorized.StatusCode.value__)" }

$login = Invoke-WebRequest "$base/user/login" -Method Post -Body @{ userName='demo'; password='demo123456' } -UseBasicParsing
Assert-Status $login.StatusCode 200 'login'
$token = ($login.Content | ConvertFrom-Json).data.token
if ([string]::IsNullOrWhiteSpace($token)) { throw 'login did not return a bearer token' }

$headers = @{ Authorization = "Bearer $token" }
$productList = Invoke-WebRequest "$base/product/list?page=1&pageSize=10&keyword=Go" -Headers $headers -UseBasicParsing
if (($productList.Content | ConvertFrom-Json).data.total -lt 1) { throw 'product pagination/search returned no fixtures' }

$foreignHeaders = @{ Authorization = "Bearer $token"; 'Idempotency-Key' = "foreign-$([guid]::NewGuid())" }
$foreign = try { Invoke-WebRequest "$base/order/create" -Method Post -Headers $foreignHeaders -Body @{ userID='2'; productID='1'; quantity='1'; orderStatus='0' } -UseBasicParsing } catch { $_.Exception.Response }
if ($foreign.StatusCode.value__ -ne 403) { throw "ownership: expected HTTP 403, got $($foreign.StatusCode.value__)" }
$headers['Idempotency-Key'] = 'integration-invalid-order'
$bad = try { Invoke-WebRequest "$base/order/create" -Method Post -Headers $headers -Body @{ userID='1'; productID='1'; quantity='0'; orderStatus='0' } -UseBasicParsing } catch { $_.Exception.Response }
if ($bad.StatusCode.value__ -ne 400) { throw "validation: expected HTTP 400, got $($bad.StatusCode.value__)" }

$beforeStock = ((Invoke-WebRequest "$base/product/1" -Headers $headers -UseBasicParsing).Content | ConvertFrom-Json).data.ProductNum
$headers['Idempotency-Key'] = "integration-order-$([guid]::NewGuid())"
$first = Invoke-WebRequest "$base/order/create" -Method Post -Headers $headers -Body @{ userID='1'; productID='1'; quantity='2'; orderStatus='0' } -UseBasicParsing
$second = Invoke-WebRequest "$base/order/create" -Method Post -Headers $headers -Body @{ userID='1'; productID='1'; quantity='2'; orderStatus='0' } -UseBasicParsing
$firstID = ($first.Content | ConvertFrom-Json).orderID
$secondID = ($second.Content | ConvertFrom-Json).orderID
if ($firstID -ne $secondID) { throw 'idempotent retries must return the same order ID' }

$orderList = (Invoke-WebRequest "$base/order/list?page=1&pageSize=10" -Headers $headers -UseBasicParsing).Content | ConvertFrom-Json
if ($orderList.data.total -lt 1) { throw 'owned order list is empty' }

$cancel = Invoke-WebRequest "$base/order/delete" -Method Post -Headers $headers -Body @{ id="$firstID" } -UseBasicParsing
Assert-Status $cancel.StatusCode 200 'cancel order'
$afterStock = ((Invoke-WebRequest "$base/product/1" -Headers $headers -UseBasicParsing).Content | ConvertFrom-Json).data.ProductNum
if ([int64]$afterStock -ne [int64]$beforeStock) { throw "cancel did not restore stock: before=$beforeStock after=$afterStock" }

$cancelAgain = try { Invoke-WebRequest "$base/order/delete" -Method Post -Headers $headers -Body @{ id="$firstID" } -UseBasicParsing } catch { $_.Exception.Response }
if ($cancelAgain.StatusCode.value__ -ne 409) { throw "state machine: expected HTTP 409, got $($cancelAgain.StatusCode.value__)" }

Write-Host 'integration smoke tests passed'
