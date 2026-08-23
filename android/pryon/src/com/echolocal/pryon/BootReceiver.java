package com.echolocal.pryon;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.util.Log;

public final class BootReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        Log.i(PryonProtocol.TAG, "PRYON_BOOT_RECEIVER action="
                + (intent == null ? "null" : intent.getAction()));
        context.startService(new Intent(context, PryonDetectorService.class));
    }
}
