<?php

namespace OPNsense\Cloudflared;

use OPNsense\Base\BaseModel;

/**
 * Class Settings
 * @package OPNsense\Cloudflared
 */
class Settings extends BaseModel
{
    /**
     * {@inheritdoc}
     */
    public function performValidation($validateFullModel = false)
    {
        $messages = parent::performValidation($validateFullModel);
        return $messages;
    }
}