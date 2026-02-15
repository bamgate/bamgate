package com.bamgate.app

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import com.bamgate.app.data.ConfigRepository
import com.bamgate.app.service.BamgateVpnService
import com.bamgate.app.service.VpnStateMonitor
import com.bamgate.app.ui.HomeScreen
import com.bamgate.app.ui.SettingsScreen
import com.bamgate.app.ui.SetupScreen
import com.bamgate.app.ui.theme.BamgateTheme
import mobile.Mobile

class MainActivity : ComponentActivity() {

    private lateinit var configRepo: ConfigRepository
    private lateinit var vpnStateMonitor: VpnStateMonitor

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            startVpnService()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        configRepo = ConfigRepository(this)
        vpnStateMonitor = VpnStateMonitor(this)

        setContent {
            BamgateTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background
                ) {
                    val hasConfig by configRepo.hasConfig.collectAsState(initial = false)
                    val configToml by configRepo.configToml.collectAsState(initial = null)
                    val isVpnActive by vpnStateMonitor.isVpnActive.collectAsState()
                    var showSettings by remember { mutableStateOf(false) }

                    when {
                        !hasConfig -> {
                            SetupScreen(
                                configRepo = configRepo,
                                onSetupComplete = { /* recompose will show HomeScreen */ }
                            )
                        }
                        showSettings -> {
                            // Read config values via Go's mobile binding.
                            val toml = configToml
                            val tunnel = remember(toml) {
                                toml?.let {
                                    try { Mobile.newTunnel(it) } catch (_: Exception) { null }
                                }
                            }

                            SettingsScreen(
                                configRepo = configRepo,
                                deviceName = tunnel?.deviceName ?: "unknown",
                                serverURL = tunnel?.serverURL ?: "unknown",
                                initialAcceptRoutes = tunnel?.acceptRoutes ?: true,
                                initialForceRelay = tunnel?.forceRelay ?: false,
                                isVpnRunning = isVpnActive,
                                onBack = { showSettings = false },
                                onConfigReset = {
                                    showSettings = false
                                    // Stop VPN if running
                                    stopVpnService()
                                },
                                onDisconnectVpn = { stopVpnService() }
                            )
                        }
                        else -> {
                            HomeScreen(
                                isConnected = isVpnActive,
                                onConnect = { requestVpnPermission() },
                                onDisconnect = { stopVpnService() },
                                onSettings = { showSettings = true }
                            )
                        }
                    }
                }
            }
        }
    }

    override fun onDestroy() {
        vpnStateMonitor.destroy()
        super.onDestroy()
    }

    private fun requestVpnPermission() {
        val intent = VpnService.prepare(this)
        if (intent != null) {
            vpnPermissionLauncher.launch(intent)
        } else {
            // Permission already granted.
            startVpnService()
        }
    }

    private fun startVpnService() {
        val intent = Intent(this, BamgateVpnService::class.java).apply {
            action = BamgateVpnService.ACTION_START
        }
        startForegroundService(intent)
    }

    private fun stopVpnService() {
        val intent = Intent(this, BamgateVpnService::class.java).apply {
            action = BamgateVpnService.ACTION_STOP
        }
        startService(intent)
    }

}
