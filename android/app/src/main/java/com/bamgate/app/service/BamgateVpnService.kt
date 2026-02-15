package com.bamgate.app.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import com.bamgate.app.MainActivity
import com.bamgate.app.R
import com.bamgate.app.data.ConfigRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import mobile.Mobile
import mobile.Logger as GoLogger
import mobile.RouteUpdateCallback as GoRouteUpdateCallback
import mobile.SocketProtector as GoSocketProtector
import org.json.JSONArray

class BamgateVpnService : VpnService() {

    companion object {
        const val ACTION_START = "com.bamgate.app.START"
        const val ACTION_STOP = "com.bamgate.app.STOP"
        private const val TAG = "BamgateVpn"
        private const val NOTIFICATION_ID = 1
        private const val CHANNEL_ID = "bamgate_vpn"
    }

    private var tunnel: mobile.Tunnel? = null
    private var tunPfd: ParcelFileDescriptor? = null // non-null until detached to Go
    private var tunnelJob: Job? = null
    private val serviceScope = CoroutineScope(Dispatchers.IO)

    // Tracks the extra routes learned from peers (e.g. "192.168.1.0/24").
    // These are added to the VPN builder on restart.
    private val peerRoutes = mutableListOf<String>()

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> startTunnel()
            ACTION_STOP -> stopTunnel()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopTunnel()
        serviceScope.cancel()
        super.onDestroy()
    }

    private fun startTunnel() {
        if (tunnel?.isRunning == true) {
            Log.w(TAG, "Tunnel is already running")
            return
        }

        // Show foreground notification.
        startForeground(NOTIFICATION_ID, buildNotification(getString(R.string.vpn_notification_connecting)))

        tunnelJob = serviceScope.launch {
            try {
                // Load config from DataStore.
                val configRepo = ConfigRepository(this@BamgateVpnService)
                val configToml = configRepo.configToml.first()
                if (configToml.isNullOrBlank()) {
                    Log.e(TAG, "No config found")
                    stopSelf()
                    return@launch
                }

                // Create the Go tunnel.
                val t = Mobile.newTunnel(configToml)
                tunnel = t

                // Set up logging.
                t.setLogger(object : GoLogger {
                    override fun log(level: Long, msg: String?) {
                        when (level.toInt()) {
                            0 -> Log.d(TAG, msg ?: "")
                            1 -> Log.i(TAG, msg ?: "")
                            2 -> Log.w(TAG, msg ?: "")
                            3 -> Log.e(TAG, msg ?: "")
                        }
                    }
                })

                // Set up socket protection.
                t.setSocketProtector(object : GoSocketProtector {
                    override fun protect(fd: Long): Boolean {
                        return this@BamgateVpnService.protect(fd.toInt())
                    }
                })

                // Set up route update callback. When the Go agent discovers
                // peer routes (e.g. home LAN subnets), we need to restart the
                // VPN with those routes added to the builder.
                t.setRouteUpdateCallback(object : GoRouteUpdateCallback {
                    override fun onRoutesUpdated(routesJSON: String?) {
                        if (routesJSON == null) return
                        Log.i(TAG, "Route update received: $routesJSON")

                        // Parse the JSON array of CIDR strings.
                        val newRoutes = mutableListOf<String>()
                        try {
                            val arr = JSONArray(routesJSON)
                            for (i in 0 until arr.length()) {
                                newRoutes.add(arr.getString(i))
                            }
                        } catch (e: Exception) {
                            Log.e(TAG, "Failed to parse routes JSON: $routesJSON", e)
                            return
                        }

                        if (newRoutes.isEmpty()) return

                        // Check if we already have all these routes.
                        synchronized(peerRoutes) {
                            val actuallyNew = newRoutes.filter { it !in peerRoutes }
                            if (actuallyNew.isEmpty()) return
                            peerRoutes.addAll(actuallyNew)
                        }

                        // Request a tunnel restart with updated routes.
                        // This is called from a Go goroutine — signal the
                        // tunnel coroutine to handle the restart.
                        Log.i(TAG, "Requesting tunnel restart for new routes")
                        t.stop()
                    }
                })

                // Parse the tunnel subnet for the initial VPN route.
                val tunnelSubnet = t.tunnelSubnet
                val tunnelAddress = t.tunnelAddress

                // Run the tunnel in a loop — it restarts when new routes arrive.
                var currentRoutes: List<String>
                var shouldRestart = true

                while (shouldRestart) {
                    // Snapshot current peer routes for this VPN establishment.
                    synchronized(peerRoutes) {
                        currentRoutes = peerRoutes.toList()
                    }

                    // Build and establish the VPN interface.
                    val rawFd = establishVpn(tunnelAddress, tunnelSubnet, currentRoutes)
                    if (rawFd < 0) {
                        Log.e(TAG, "VPN interface creation failed")
                        break
                    }

                    // Update notification.
                    updateNotification(getString(R.string.vpn_notification_connected))

                    // Start the Go tunnel (blocks until stopped).
                    try {
                        t.start(rawFd.toLong())
                        Log.i(TAG, "Tunnel stopped")
                    } catch (e: Exception) {
                        Log.e(TAG, "Tunnel error: ${e.message}")
                    }

                    // Check if this was a route-triggered restart or a real stop.
                    // If new routes were added since we last established the VPN,
                    // we should restart. Otherwise, we're done.
                    synchronized(peerRoutes) {
                        shouldRestart = peerRoutes.size > currentRoutes.size
                    }

                    if (shouldRestart) {
                        Log.i(TAG, "Restarting tunnel with updated routes: $peerRoutes")
                        updateNotification(getString(R.string.vpn_notification_connecting))
                    }
                }

                // Tunnel stopped for real.
                updateNotification(getString(R.string.vpn_notification_disconnected))
            } catch (e: Exception) {
                Log.e(TAG, "Failed to start tunnel", e)
            } finally {
                cleanup()
                stopSelf()
            }
        }
    }

    /**
     * Establishes (or re-establishes) the VPN interface with the given routes.
     * Returns the raw file descriptor for the TUN device, or -1 on failure.
     */
    private fun establishVpn(
        tunnelAddress: String,
        tunnelSubnet: String,
        extraRoutes: List<String>
    ): Int {
        val (addrIp, addrPrefix) = parseCIDR(tunnelAddress)
        val (subnetIp, subnetPrefix) = parseCIDR(tunnelSubnet)

        val builder = Builder()
            .setSession("bamgate")
            .setMtu(1420)
            .addAddress(addrIp, addrPrefix)
            .addRoute(subnetIp, subnetPrefix) // Tunnel subnet (e.g. 10.0.0.0/24)
            .addDnsServer("8.8.8.8")
            .addDnsServer("8.8.4.4")

        // Add peer-advertised routes (e.g. 192.168.1.0/24 for home LAN).
        for (route in extraRoutes) {
            try {
                val (routeIp, routePrefix) = parseCIDR(route)
                builder.addRoute(routeIp, routePrefix)
                Log.i(TAG, "VPN route added: $route")
            } catch (e: Exception) {
                Log.w(TAG, "Skipping invalid route: $route", e)
            }
        }

        // Exclude the bamgate app itself from VPN routing to prevent loops
        // with the signaling WebSocket (belt-and-suspenders with protect()).
        builder.addDisallowedApplication(packageName)

        val pfd = builder.establish() ?: return -1

        // detachFd() transfers ownership of the raw fd to Go.
        // After this, Java must NOT close the ParcelFileDescriptor —
        // Go/wireguard-go will close the fd when the tunnel stops.
        val rawFd = pfd.detachFd()
        tunPfd = null // we no longer own it

        Log.i(TAG, "VPN established with routes: subnet=$tunnelSubnet extra=$extraRoutes")
        return rawFd
    }

    private fun stopTunnel() {
        // Clear peer routes so restart loop exits.
        synchronized(peerRoutes) {
            peerRoutes.clear()
        }
        tunnel?.stop()
        tunnelJob?.cancel()
        cleanup()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun cleanup() {
        // tunPfd is only non-null if establish() succeeded but we haven't
        // yet detached the fd to Go. After detachFd(), Go owns the fd and
        // closes it when the tunnel stops — we must not double-close.
        tunPfd?.close()
        tunPfd = null
        tunnel = null
    }

    private fun parseCIDR(cidr: String): Pair<String, Int> {
        val parts = cidr.split("/")
        val ip = parts[0]
        val prefix = if (parts.size > 1) parts[1].toIntOrNull() ?: 24 else 24
        return Pair(ip, prefix)
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                getString(R.string.vpn_notification_channel),
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "bamgate VPN connection status"
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(status: String): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE
        )

        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.vpn_notification_title))
            .setContentText(status)
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentIntent(pendingIntent)
            .setOngoing(true)
            .build()
    }

    private fun updateNotification(status: String) {
        val manager = getSystemService(NotificationManager::class.java)
        manager.notify(NOTIFICATION_ID, buildNotification(status))
    }
}
