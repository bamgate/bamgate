package com.bamgate.app.service

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Monitors whether an active VPN network exists on the device by listening
 * to [ConnectivityManager] callbacks. Exposes the state as a [StateFlow]
 * that Compose can collect directly — no manual bookkeeping required.
 */
class VpnStateMonitor(context: Context) {

    private val connectivityManager =
        context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    private val _isVpnActive = MutableStateFlow(checkCurrentVpnState())
    val isVpnActive: StateFlow<Boolean> = _isVpnActive.asStateFlow()

    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            _isVpnActive.value = true
        }

        override fun onLost(network: Network) {
            _isVpnActive.value = false
        }
    }

    init {
        val request = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_VPN)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        connectivityManager.registerNetworkCallback(request, networkCallback)
    }

    fun destroy() {
        connectivityManager.unregisterNetworkCallback(networkCallback)
    }

    private fun checkCurrentVpnState(): Boolean {
        val activeNetwork = connectivityManager.activeNetwork ?: return false
        val caps = connectivityManager.getNetworkCapabilities(activeNetwork) ?: return false
        return caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)
    }
}
