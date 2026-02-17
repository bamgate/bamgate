package com.bamgate.app.service

import mobile.Tunnel

/**
 * Singleton holder for the live [Tunnel] instance running in [BamgateVpnService].
 *
 * The VPN service sets the tunnel when it starts and clears it when it stops.
 * The UI layer (DevicesScreen) reads from here to access the running agent's
 * peer offerings, DNS config, etc.
 *
 * This is safe because Android runs the service and activity in the same
 * process by default (no android:process attribute in the manifest).
 */
object TunnelHolder {
    @Volatile
    var tunnel: Tunnel? = null
        private set

    fun set(t: Tunnel) {
        tunnel = t
    }

    fun clear() {
        tunnel = null
    }
}
