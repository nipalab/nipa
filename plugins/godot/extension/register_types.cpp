#include "register_types.h"

#include <gdextension_interface.h>

#include <godot_cpp/core/class_db.hpp>
#include <godot_cpp/classes/engine.hpp>
#include <godot_cpp/godot.hpp>

#include "nipa_request.h"
#include "nipa_session.h"
#include "nipa_vcs_interface.h"

using namespace godot;
using namespace nipa::glue;

namespace {

NipaSession* session_singleton = nullptr;

}  // namespace

void initialize_nipa_module(ModuleInitializationLevel p_level) {
    if (p_level != MODULE_INITIALIZATION_LEVEL_EDITOR) {
        return;
    }
    GDREGISTER_CLASS(NipaRequest);
    GDREGISTER_CLASS(NipaSession);
    GDREGISTER_CLASS(NipaVCSInterface);
    session_singleton = memnew(NipaSession);
    Engine::get_singleton()->register_singleton("Nipa", session_singleton);
}

void uninitialize_nipa_module(ModuleInitializationLevel p_level) {
    if (p_level != MODULE_INITIALIZATION_LEVEL_EDITOR) {
        return;
    }
    Engine::get_singleton()->unregister_singleton("Nipa");
    memdelete(session_singleton);
    session_singleton = nullptr;
}

extern "C" GDExtensionBool GDE_EXPORT nipa_library_init(GDExtensionInterfaceGetProcAddress p_get_proc_address,
                                                        const GDExtensionClassLibraryPtr p_library,
                                                        GDExtensionInitialization* r_initialization) {
    GDExtensionBinding::InitObject init_obj(p_get_proc_address, p_library, r_initialization);
    init_obj.register_initializer(initialize_nipa_module);
    init_obj.register_terminator(uninitialize_nipa_module);
    init_obj.set_minimum_library_initialization_level(MODULE_INITIALIZATION_LEVEL_EDITOR);
    return init_obj.init();
}
