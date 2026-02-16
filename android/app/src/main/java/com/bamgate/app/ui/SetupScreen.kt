package com.bamgate.app.ui

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.bamgate.app.data.ConfigRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile
import org.json.JSONObject
import java.io.OutputStreamWriter
import java.net.HttpURLConnection
import java.net.URL

// GitHub OAuth Device Authorization Grant client ID (public, no secret needed).
private const val GITHUB_CLIENT_ID = "Ov23liOEzb4I8AiZupu2"

private data class DeviceCodeResponse(
    val deviceCode: String,
    val userCode: String,
    val verificationUri: String,
    val expiresIn: Int,
    val interval: Int
)

/**
 * Calls GitHub's POST /login/device/code to start the Device Auth flow.
 * Must be called on a background thread.
 */
private fun requestDeviceCode(): DeviceCodeResponse {
    val url = URL("https://github.com/login/device/code")
    val conn = url.openConnection() as HttpURLConnection
    conn.requestMethod = "POST"
    conn.setRequestProperty("Content-Type", "application/x-www-form-urlencoded")
    conn.setRequestProperty("Accept", "application/json")
    conn.doOutput = true

    val body = "client_id=$GITHUB_CLIENT_ID&scope=read:user"
    OutputStreamWriter(conn.outputStream).use { it.write(body) }

    val responseCode = conn.responseCode
    val responseBody = conn.inputStream.bufferedReader().readText()
    conn.disconnect()

    if (responseCode != 200) {
        throw Exception("GitHub device/code failed: HTTP $responseCode")
    }

    val json = JSONObject(responseBody)
    return DeviceCodeResponse(
        deviceCode = json.getString("device_code"),
        userCode = json.getString("user_code"),
        verificationUri = json.getString("verification_uri"),
        expiresIn = json.getInt("expires_in"),
        interval = json.getInt("interval")
    )
}

/**
 * Polls GitHub's POST /login/oauth/access_token until the user authorizes.
 * Returns the access token string, or throws on error/expiry.
 * Must be called on a background thread.
 */
private fun pollForAccessToken(deviceCode: String, intervalSeconds: Int, expiresIn: Int): String {
    var interval = maxOf(intervalSeconds, 5).toLong()
    val deadline = System.currentTimeMillis() + expiresIn * 1000L

    while (System.currentTimeMillis() < deadline) {
        Thread.sleep(interval * 1000)

        val url = URL("https://github.com/login/oauth/access_token")
        val conn = url.openConnection() as HttpURLConnection
        conn.requestMethod = "POST"
        conn.setRequestProperty("Content-Type", "application/x-www-form-urlencoded")
        conn.setRequestProperty("Accept", "application/json")
        conn.doOutput = true

        val body = "client_id=$GITHUB_CLIENT_ID&device_code=$deviceCode&grant_type=urn:ietf:params:oauth:grant-type:device_code"
        OutputStreamWriter(conn.outputStream).use { it.write(body) }

        val responseBody = conn.inputStream.bufferedReader().readText()
        conn.disconnect()

        val json = JSONObject(responseBody)
        val error = json.optString("error", "")

        when (error) {
            "" -> {
                val token = json.getString("access_token")
                if (token.isNotEmpty()) return token
            }
            "authorization_pending" -> continue
            "slow_down" -> {
                interval += 5
                continue
            }
            "expired_token" -> throw Exception("Authorization expired. Please try again.")
            "access_denied" -> throw Exception("Authorization was denied.")
            else -> throw Exception("GitHub error: $error")
        }
    }

    throw Exception("Authorization expired. Please try again.")
}

@Composable
fun SetupScreen(
    configRepo: ConfigRepository,
    onSetupComplete: () -> Unit
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val snackbarHostState = remember { SnackbarHostState() }

    var serverHost by remember { mutableStateOf("") }
    var deviceName by remember { mutableStateOf(Build.MODEL) }
    var error by remember { mutableStateOf<String?>(null) }

    // Auth flow state.
    var isRequestingCode by remember { mutableStateOf(false) }
    var userCode by remember { mutableStateOf<String?>(null) }
    var verificationUri by remember { mutableStateOf<String?>(null) }
    var isPolling by remember { mutableStateOf(false) }
    var isRegistering by remember { mutableStateOf(false) }

    // QR scanner state.
    var showScanner by remember { mutableStateOf(false) }

    val isBusy = isRequestingCode || isPolling || isRegistering

    // Camera permission launcher.
    val cameraPermissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        if (granted) {
            showScanner = true
        }
    }

    // Show QR scanner if requested.
    if (showScanner) {
        QrScannerScreen(
            onCodeScanned = { scanned ->
                showScanner = false
                serverHost = scanned
            },
            onCancel = { showScanner = false }
        )
        return
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbarHostState) }
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(32.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center
        ) {
            Text(
                text = "bamgate",
                style = MaterialTheme.typography.headlineLarge,
                fontWeight = FontWeight.Bold
            )

            Spacer(modifier = Modifier.height(8.dp))

            Text(
                text = "Set up your VPN connection",
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )

            Spacer(modifier = Modifier.height(32.dp))

            // If we have a user code to show, display the verification UI.
            if (userCode != null && verificationUri != null) {
                Text(
                    text = "Enter this code on GitHub:",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )

                Spacer(modifier = Modifier.height(12.dp))

                // Tappable code — copies to clipboard on tap.
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.Center,
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable {
                            val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                            clipboard.setPrimaryClip(ClipData.newPlainText("bamgate code", userCode))
                            scope.launch {
                                snackbarHostState.showSnackbar("Code copied to clipboard")
                            }
                        }
                        .padding(vertical = 8.dp)
                ) {
                    Text(
                        text = userCode!!,
                        style = MaterialTheme.typography.headlineMedium.copy(
                            fontFamily = FontFamily.Monospace,
                            letterSpacing = 4.sp
                        ),
                        fontWeight = FontWeight.Bold,
                        textAlign = TextAlign.Center
                    )
                    Spacer(modifier = Modifier.width(8.dp))
                    Icon(
                        painter = painterResource(id = android.R.drawable.ic_menu_upload),
                        contentDescription = "Copy code",
                        modifier = Modifier.size(20.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }

                Text(
                    text = "Tap to copy",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )

                Spacer(modifier = Modifier.height(16.dp))

                OutlinedButton(
                    onClick = {
                        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(verificationUri))
                        context.startActivity(intent)
                    },
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("Open GitHub")
                }

                Spacer(modifier = Modifier.height(16.dp))

                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.Center,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    CircularProgressIndicator(
                        modifier = Modifier
                            .height(16.dp)
                            .width(16.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Spacer(modifier = Modifier.width(8.dp))
                    Text(
                        text = if (isRegistering) "Registering device..." else "Waiting for authorization...",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            } else {
                // Initial form: server host + device name + sign in button.
                OutlinedTextField(
                    value = serverHost,
                    onValueChange = { serverHost = it },
                    label = { Text("Server host") },
                    placeholder = { Text("bamgate.xxxxx.workers.dev") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    enabled = !isBusy,
                    trailingIcon = {
                        IconButton(
                            onClick = {
                                cameraPermissionLauncher.launch(Manifest.permission.CAMERA)
                            },
                            enabled = !isBusy
                        ) {
                            Icon(
                                painter = painterResource(id = android.R.drawable.ic_menu_camera),
                                contentDescription = "Scan QR code",
                                tint = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                )

                Spacer(modifier = Modifier.height(8.dp))

                OutlinedTextField(
                    value = deviceName,
                    onValueChange = { deviceName = it },
                    label = { Text("Device name") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    enabled = !isBusy
                )

                Spacer(modifier = Modifier.height(24.dp))

                Button(
                    onClick = {
                        error = null
                        isRequestingCode = true

                        scope.launch {
                            try {
                                // Step 1: Request device code from GitHub.
                                val dcResp = withContext(Dispatchers.IO) {
                                    requestDeviceCode()
                                }

                                isRequestingCode = false
                                userCode = dcResp.userCode
                                verificationUri = dcResp.verificationUri
                                isPolling = true

                                // Auto-copy code to clipboard for convenience.
                                val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                                clipboard.setPrimaryClip(ClipData.newPlainText("bamgate code", dcResp.userCode))
                                snackbarHostState.showSnackbar("Code copied to clipboard")

                                // Step 2: Poll for GitHub authorization.
                                val githubToken = withContext(Dispatchers.IO) {
                                    pollForAccessToken(
                                        dcResp.deviceCode,
                                        dcResp.interval,
                                        dcResp.expiresIn
                                    )
                                }

                                isPolling = false
                                isRegistering = true

                                // Step 3: Register device with bamgate server.
                                val result = withContext(Dispatchers.IO) {
                                    Mobile.registerDevice(
                                        serverHost,
                                        githubToken,
                                        deviceName
                                    )
                                }

                                // Step 4: Save config and finish.
                                configRepo.saveConfig(result.configTOML)
                                onSetupComplete()
                            } catch (e: Exception) {
                                error = e.message ?: "Setup failed"
                                isRequestingCode = false
                                isPolling = false
                                isRegistering = false
                                userCode = null
                                verificationUri = null
                            }
                        }
                    },
                    enabled = serverHost.isNotBlank() && deviceName.isNotBlank() && !isBusy,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    if (isRequestingCode) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            CircularProgressIndicator(
                                modifier = Modifier
                                    .height(20.dp)
                                    .width(20.dp),
                                strokeWidth = 2.dp,
                                color = MaterialTheme.colorScheme.onPrimary
                            )
                            Spacer(modifier = Modifier.width(8.dp))
                            Text("Connecting...")
                        }
                    } else {
                        Text("Sign in with GitHub")
                    }
                }
            }

            // Error display.
            error?.let {
                Spacer(modifier = Modifier.height(16.dp))
                Text(
                    text = it,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall
                )
            }
        }
    }
}
