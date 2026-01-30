<?php

namespace OPNsense\Cloudflared\Api;

use OPNsense\Base\ApiMutableServiceControllerBase;
use OPNsense\Core\Backend;

/**
 * Class ServiceController
 * @package OPNsense\Cloudflared\Api
 */
class ServiceController extends ApiMutableServiceControllerBase
{
    protected static $internalServiceClass = '\OPNsense\Cloudflared\Settings';
    protected static $internalServiceEnabled = 'general.enabled';
    protected static $internalServiceName = 'cloudflared';

    /**
     * Get cloudflared version
     * @return array version information
     */
    public function versionAction()
    {
        $backend = new Backend();
        $response = trim($backend->configdRun('cloudflared version'));
        if (preg_match('/cloudflared version ([0-9.]+)/', $response, $matches)) {
            return ['version' => $matches[1]];
        }
        return ['version' => 'unknown'];
    }

    /**
     * Get cloudflared service status
     * @return array status information
     */
    public function statusAction()
    {
        $backend = new Backend();
        $response = trim($backend->configdRun('cloudflared status'));
        $running = strpos($response, 'is running') !== false;
        return [
            'status' => $running ? 'running' : 'stopped',
            'message' => $response
        ];
    }
}
