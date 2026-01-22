<?php

namespace OPNsense\Cloudflared\Api;

use OPNsense\Base\ApiMutableModelControllerBase;

/**
 * Class SettingsController
 * @package OPNsense\Cloudflared\Api
 */
class SettingsController extends ApiMutableModelControllerBase
{
    protected static $internalModelClass = '\OPNsense\Cloudflared\Settings';
    protected static $internalModelName = 'settings';
}