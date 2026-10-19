<?php
declare(strict_types=1);

require_once('/usr/local/opnsense/mvc/script/load_phalcon.php');

use OPNsense\Cloudflared\Settings;

if ($argc !== 3) {
    throw new RuntimeException('Provide the plugin model file and a protected tunnel token file.');
}

require_once($argv[1]);

$token = file_get_contents($argv[2]);
if ($token === false) {
    throw new RuntimeException('Cannot read the tunnel token file.');
}
$token = trim($token);
if ($token === '') {
    throw new RuntimeException('The tunnel token file is empty.');
}

$settings = new Settings();
$settings->general->enabled = '1';
$settings->general->mode = 'token';
$settings->general->token = $token;

if (count($settings->validate(null, '', true)) !== 0) {
    throw new RuntimeException('The plugin rejected the Cloudflare tunnel token.');
}

$settings->general->token = 'not a tunnel token';
$messages = $settings->validate(null, '', true);
if (!isset($messages['general.token'])) {
    throw new RuntimeException('The plugin accepted invalid token text.');
}

fwrite(STDOUT, "The plugin accepts the Cloudflare token and rejects invalid token text.\n");
