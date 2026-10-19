<?php
declare(strict_types=1);

require_once('/usr/local/opnsense/mvc/script/load_phalcon.php');

use OPNsense\Cloudflared\Settings;

function fail_test(string $message): never
{
    fwrite(STDERR, $message . PHP_EOL);
    exit(1);
}

if ($argc !== 3) {
    fail_test('Provide the plugin model file and a protected tunnel token file.');
}

if (!is_file($argv[1]) || !is_readable($argv[1])) {
    fail_test('Provide a readable plugin model file.');
}
if (!is_file($argv[2]) || !is_readable($argv[2])) {
    fail_test('Provide a readable tunnel token file.');
}
require_once($argv[1]);

$token = file_get_contents($argv[2]);
if ($token === false) {
    fail_test('Cannot read the tunnel token file.');
}
$token = trim($token);
if ($token === '') {
    fail_test('The tunnel token file is empty.');
}

$settings = new Settings();
$settings->general->enabled = '1';
$settings->general->mode = 'token';
$settings->general->token = $token;

if (count($settings->validate(null, '', true)) !== 0) {
    fail_test('The plugin rejected the Cloudflare tunnel token.');
}

$settings->general->token = 'not a tunnel token';
$messages = $settings->validate(null, '', true);
if (!isset($messages['general.token'])) {
    fail_test('The plugin accepted invalid token text.');
}

fwrite(STDOUT, "The plugin accepts the Cloudflare token and rejects invalid token text.\n");
