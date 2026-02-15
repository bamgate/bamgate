package com.bamgate.app.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.bamgate.app.data.ConfigRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    configRepo: ConfigRepository,
    deviceName: String,
    serverURL: String,
    initialAcceptRoutes: Boolean,
    initialForceRelay: Boolean,
    isVpnRunning: Boolean,
    onBack: () -> Unit,
    onConfigReset: () -> Unit,
    onDisconnectVpn: () -> Unit
) {
    val scope = rememberCoroutineScope()
    var acceptRoutes by remember { mutableStateOf(initialAcceptRoutes) }
    var forceRelay by remember { mutableStateOf(initialForceRelay) }
    var showResetDialog by remember { mutableStateOf(false) }
    var errorMessage by remember { mutableStateOf<String?>(null) }

    // Helper to update a toggle in the TOML config via Go's UpdateConfig.
    fun updateToggle(field: String, value: Boolean) {
        scope.launch {
            try {
                val currentToml = configRepo.configToml.first() ?: return@launch
                // Create a temporary tunnel to use UpdateConfig
                val tunnel = withContext(Dispatchers.IO) {
                    Mobile.newTunnel(currentToml)
                }

                // Build updated TOML by parsing, modifying the field, and re-marshaling.
                // We modify the TOML text directly since the Go side handles the
                // round-trip via UpdateConfig.
                val updatedToml = when (field) {
                    "accept_routes" -> updateTomlBool(currentToml, "accept_routes", value)
                    "force_relay" -> updateTomlBool(currentToml, "force_relay", value)
                    else -> currentToml
                }

                // Send through Go for validation and canonical re-marshaling.
                val canonical = withContext(Dispatchers.IO) {
                    tunnel.updateConfig(updatedToml)
                }
                configRepo.saveConfig(canonical)
                errorMessage = null
                // Disconnect the VPN so the user reconnects with the new setting.
                if (isVpnRunning) {
                    onDisconnectVpn()
                }
            } catch (e: Exception) {
                errorMessage = e.message
                // Revert the UI toggle on failure
                when (field) {
                    "accept_routes" -> acceptRoutes = !value
                    "force_relay" -> forceRelay = !value
                }
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = "Back"
                        )
                    }
                }
            )
        }
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(horizontal = 16.dp)
                .verticalScroll(rememberScrollState())
        ) {
            // --- Device Info Section ---
            Text(
                text = "Device",
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(top = 16.dp, bottom = 8.dp)
            )

            InfoRow(label = "Name", value = deviceName)
            InfoRow(label = "Server", value = serverURL)

            HorizontalDivider(modifier = Modifier.padding(vertical = 16.dp))

            // --- Routing Section ---
            Text(
                text = "Routing",
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(bottom = 8.dp)
            )

            ToggleRow(
                title = "Accept Routes",
                description = "Install subnet routes advertised by remote peers (e.g. home LAN access).",
                checked = acceptRoutes,
                onCheckedChange = { newValue ->
                    acceptRoutes = newValue
                    updateToggle("accept_routes", newValue)
                }
            )

            Spacer(modifier = Modifier.height(8.dp))

            ToggleRow(
                title = "Force Relay",
                description = "Force all connections through the TURN relay. Useful for testing or when direct connectivity fails.",
                checked = forceRelay,
                onCheckedChange = { newValue ->
                    forceRelay = newValue
                    updateToggle("force_relay", newValue)
                }
            )

            HorizontalDivider(modifier = Modifier.padding(vertical = 16.dp))

            // --- Danger Zone ---
            Text(
                text = "Danger Zone",
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(bottom = 8.dp)
            )

            OutlinedButton(
                onClick = { showResetDialog = true },
                modifier = Modifier.fillMaxWidth()
            ) {
                Text("Reset Configuration")
            }

            Text(
                text = "Removes all configuration and returns to the setup screen. You will need to scan a new invite QR code.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp, bottom = 16.dp)
            )

            // --- Error display ---
            errorMessage?.let { msg ->
                Text(
                    text = "Error: $msg",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(bottom = 16.dp)
                )
            }
        }
    }

    // Reset confirmation dialog
    if (showResetDialog) {
        AlertDialog(
            onDismissRequest = { showResetDialog = false },
            title = { Text("Reset Configuration?") },
            text = { Text("This will remove your device configuration. You will need to scan a new invite QR code to reconnect.") },
            confirmButton = {
                TextButton(
                    onClick = {
                        showResetDialog = false
                        scope.launch {
                            configRepo.clearConfig()
                            onConfigReset()
                        }
                    }
                ) {
                    Text("Reset", color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = {
                TextButton(onClick = { showResetDialog = false }) {
                    Text("Cancel")
                }
            }
        )
    }
}

@Composable
private fun InfoRow(label: String, value: String) {
    Column(modifier = Modifier.padding(vertical = 4.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            fontFamily = FontFamily.Monospace
        )
    }
}

@Composable
private fun ToggleRow(
    title: String,
    description: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyLarge
            )
            Text(
                text = description,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            modifier = Modifier.padding(start = 8.dp)
        )
    }
}

/**
 * Updates a boolean field in a TOML string. If the field exists, its value is
 * replaced. If not, it is appended under the [device] section.
 */
private fun updateTomlBool(toml: String, key: String, value: Boolean): String {
    val regex = Regex("""(?m)^\s*${Regex.escape(key)}\s*=\s*(true|false)\s*$""")
    return if (regex.containsMatchIn(toml)) {
        regex.replace(toml) { "$key = $value" }
    } else {
        // Append after [device] section header
        val deviceSection = Regex("""(?m)^\[device]""")
        val match = deviceSection.find(toml)
        if (match != null) {
            val insertPos = toml.indexOf('\n', match.range.last)
            if (insertPos >= 0) {
                toml.substring(0, insertPos + 1) +
                    "$key = $value\n" +
                    toml.substring(insertPos + 1)
            } else {
                "$toml\n$key = $value\n"
            }
        } else {
            "$toml\n[device]\n$key = $value\n"
        }
    }
}
