# live testdata

Every file here is **synthesised**, not recorded: the series follow the metric and label
names of the exporters the presets target (arista-eos-exporter style, mktxp, pfsense-exporter,
node-exporter), with invented values. Nothing comes from a real network.

| File | Content |
|---|---|
| `lab.json` | Series for `FakePrometheus`: one switch (`192.0.2.11`, ports `Ethernet1`, `Et2`, two members of a LAG, one unknown port, one unknown device, one series of another job), one router (`192.0.2.21`), one firewall (`fw-01.example.net`), one server (`SRV-01`, with a second, lower series for `eno1` as a duplicate scrape gives, and `lo` and a `veth` port the node preset excludes). Counters carry their rate per second, gauges their value. Addresses are from the documentation range 192.0.2.0/24. |
