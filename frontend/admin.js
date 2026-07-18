const [config, musicDirs, bannedIPs, rooms, scan] = await Promise.all([
  import("./admin/config.js"),
  import("./admin/music-dirs.js"),
  import("./admin/banned-ips.js"),
  import("./admin/rooms.js"),
  import("./admin/scan.js"),
]);

config.init();
musicDirs.init();
bannedIPs.init();
rooms.init();
scan.init();

config.loadConfig();
scan.loadLibraryStatus();
