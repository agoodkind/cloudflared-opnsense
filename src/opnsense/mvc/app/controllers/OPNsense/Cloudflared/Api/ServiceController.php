<?php

namespace OPNsense\Cloudflared\Api;

use OPNsense\Base\ApiMutableServiceControllerBase;

/**
 * Class ServiceController
 * @package OPNsense\Cloudflared\Api
 */
class ServiceController extends ApiMutableServiceControllerBase
{
    protected static $internalServiceClass = '\OPNsense\Cloudflared\Settings';
    protected static $internalServiceEnabled = 'general.enabled';
    protected static $internalServiceName = 'cloudflared';
}