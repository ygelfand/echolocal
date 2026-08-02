package config

// Bluetooth is whether the device also acts as a BLE proxy for Home Assistant.
type Bluetooth struct {
	Proxy bool `json:"proxy"`
}

// Off unless asked for: it keeps a radio scanning that shares an antenna with wifi.
const DefaultBluetoothProxy = false

func defaultBluetooth() Bluetooth {
	return Bluetooth{Proxy: DefaultBluetoothProxy}
}

type BluetoothWriter struct{ st *Store }

func (w BluetoothWriter) Proxy(v bool) error {
	return w.st.Update(func(c *Config) { c.Bluetooth.Proxy = v })
}
