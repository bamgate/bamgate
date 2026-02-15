package com.bamgate.app.ui

import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.bamgate.app.data.ConfigRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

@Composable
fun SetupScreen(
    configRepo: ConfigRepository,
    onSetupComplete: () -> Unit
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var serverHost by remember { mutableStateOf("") }
    var inviteCode by remember { mutableStateOf("") }
    var deviceName by remember { mutableStateOf(Build.MODEL) }
    var isLoading by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var showScanner by remember { mutableStateOf(false) }

    // Check for pending invite from QR deep link.
    LaunchedEffect(Unit) {
        val pending = configRepo.consumePendingInvite()
        if (pending != null) {
            serverHost = pending.first
            inviteCode = pending.second
        }
    }

    // Camera permission for QR scanning.
    val cameraPermissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        if (granted) {
            showScanner = true
        }
    }

    if (showScanner) {
        QrScannerScreen(
            onCodeScanned = { scannedUrl ->
                showScanner = false
                // Parse bamgate://invite?server=...&code=...
                try {
                    val uri = android.net.Uri.parse(scannedUrl)
                    if (uri.scheme == "bamgate" && uri.host == "invite") {
                        serverHost = uri.getQueryParameter("server") ?: ""
                        inviteCode = uri.getQueryParameter("code") ?: ""
                    }
                } catch (_: Exception) {
                    error = "Invalid QR code"
                }
            },
            onCancel = { showScanner = false }
        )
        return
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
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

        // Scan QR button
        OutlinedButton(
            onClick = {
                cameraPermissionLauncher.launch(Manifest.permission.CAMERA)
            },
            modifier = Modifier.fillMaxWidth()
        ) {
            Text("Scan QR Code")
        }

        Spacer(modifier = Modifier.height(16.dp))

        Text(
            text = "or enter manually",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )

        Spacer(modifier = Modifier.height(16.dp))

        OutlinedTextField(
            value = serverHost,
            onValueChange = { serverHost = it },
            label = { Text("Server host") },
            placeholder = { Text("bamgate.xxxxx.workers.dev") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )

        Spacer(modifier = Modifier.height(8.dp))

        OutlinedTextField(
            value = inviteCode,
            onValueChange = { inviteCode = it },
            label = { Text("Invite code") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )

        Spacer(modifier = Modifier.height(8.dp))

        OutlinedTextField(
            value = deviceName,
            onValueChange = { deviceName = it },
            label = { Text("Device name") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )

        Spacer(modifier = Modifier.height(24.dp))

        error?.let {
            Text(
                text = it,
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodySmall
            )
            Spacer(modifier = Modifier.height(8.dp))
        }

        Button(
            onClick = {
                error = null
                isLoading = true
                scope.launch {
                    try {
                        val result = withContext(Dispatchers.IO) {
                            Mobile.redeemInvite(serverHost, inviteCode, deviceName)
                        }
                        configRepo.saveConfig(result.configTOML)
                        onSetupComplete()
                    } catch (e: Exception) {
                        error = e.message ?: "Setup failed"
                    } finally {
                        isLoading = false
                    }
                }
            },
            enabled = serverHost.isNotBlank() && inviteCode.isNotBlank() && deviceName.isNotBlank() && !isLoading,
            modifier = Modifier.fillMaxWidth()
        ) {
            if (isLoading) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(
                        modifier = Modifier
                            .height(20.dp)
                            .width(20.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.onPrimary
                    )
                    Spacer(modifier = Modifier.width(8.dp))
                    Text("Setting up...")
                }
            } else {
                Text("Join Network")
            }
        }
    }
}
